package auditclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// ingestPath is the Product-1 (AI-agent, searchable) ingest route.
	ingestPath = "/logs/ingest"
	// statusAccepted (202) is the audit service's success code for an accepted event.
	statusAccepted = http.StatusAccepted
)

// Defaults mirror the reference audit client.
const (
	defaultTimeout   = 5 * time.Second
	defaultQueueSize = 1024
	defaultWorkers   = 4
)

var defaultUserAgent = "ab0t-audit-sdk-go/" + Version

// IngestError is returned by LogEvent when the audit service answers with a
// non-202 status. Per the mesh hardening rule (R-A3-2) it carries ONLY the status
// code and a correlation id — never the response body, which could echo request
// fields.
type IngestError struct {
	StatusCode int
	RequestID  string
}

func (e *IngestError) Error() string {
	return fmt.Sprintf("auditclient: audit service returned status %d (request_id=%q)", e.StatusCode, e.RequestID)
}

// Emitter is the audit-emission port. Depend on this interface (not *Client) so
// call sites are testable against Noop / a fake with no live audit service.
type Emitter interface {
	// LogEvent SYNCHRONOUSLY marshals and POSTs the event, returning the outcome.
	LogEvent(ctx context.Context, ev AuditEventCreate) error
	// Emit hands the event to the background queue and returns immediately. It
	// never blocks or fails the caller; every failure mode is swallowed + logged.
	Emit(ev AuditEventCreate)
	// EmitRaw hands ALREADY-MARSHALLED JSON bytes to the background queue,
	// bypassing AuditEventCreate construction + marshalling. It is for callers
	// that must control the EXACT on-wire body (see the method doc on Client).
	// Same fire-and-forget semantics as Emit.
	EmitRaw(body []byte)
	// Close stops accepting new events, drains the queue (bounded by ctx), and
	// waits for the workers. Idempotent.
	Close(ctx context.Context) error
}

// Client is the HTTP audit emitter. It is safe for concurrent use.
type Client struct {
	baseURL   string
	apiKey    string
	userAgent string
	http      *http.Client
	log       *slog.Logger
	now       func() time.Time

	queue   chan []byte
	wg      sync.WaitGroup
	closeMu sync.Mutex
	closed  bool
	dropped atomic.Int64
}

var _ Emitter = (*Client)(nil)

// New builds a Client and starts its background workers. Options are applied on
// top of cfg, so they override struct fields.
func New(cfg Config, opts ...Option) *Client {
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		// No redirect-follow on the mesh client: a 3xx must NOT forward the audit
		// X-API-Key to a redirect target (CLASS-5 credential-leak guard).
		client = &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	qs := cfg.QueueSize
	if qs <= 0 {
		qs = defaultQueueSize
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}

	c := &Client{
		baseURL:   rstripSlash(cfg.URL),
		apiKey:    cfg.APIKey,
		userAgent: ua,
		http:      client,
		log:       log,
		now:       nowFn,
		queue:     make(chan []byte, qs),
	}
	c.startWorkers(workers)
	log.Info("audit client initialized",
		"base_url", c.baseURL, "has_service_key", cfg.APIKey != "",
		"queue_size", qs, "workers", workers)
	return c
}

// BaseURL returns the configured (slash-trimmed) base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Dropped reports how many events Emit dropped on queue saturation / after Close.
func (c *Client) Dropped() int64 { return c.dropped.Load() }

// LogEvent synchronously validates, marshals and POSTs the event. It returns
// ErrMissingRequiredField for a bad envelope, an *IngestError on a non-202, the
// transport error on a network failure, or nil on 202.
func (c *Client) LogEvent(ctx context.Context, ev AuditEventCreate) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	body, err := c.marshal(ev)
	if err != nil {
		return err
	}
	return c.post(ctx, body)
}

// Emit hands the event to the background queue and returns immediately. An
// invalid envelope, a marshal error, a full queue, or a closed client all result
// in the event being dropped and logged — never an error to the caller.
func (c *Client) Emit(ev AuditEventCreate) {
	if err := ev.Validate(); err != nil {
		c.log.Warn("audit event dropped: invalid envelope", "error", err.Error())
		return
	}
	body, err := c.marshal(ev)
	if err != nil {
		c.log.Warn("audit marshal failed (swallowed)", "error", err.Error())
		return
	}
	c.enqueue(body)
}

// EmitRaw hands ALREADY-MARSHALLED JSON bytes to the background queue, bypassing
// AuditEventCreate construction + marshalling. It exists for callers that must
// control the EXACT on-wire body — e.g. a service whose payload field ORDER or
// explicit-null policy is byte-parity-locked and differs from AuditEventCreate's
// (e.g. a caller that reproduces a specific dict order and sends explicit `null`s,
// which AuditEventCreate's omitempty struct tags cannot). Same
// fire-and-forget semantics as Emit: a full queue or a closed client DROPS (counted
// via Dropped, logged); the body is POSTed to {URL}/logs/ingest with the same
// X-API-Key + no-redirect guard. The caller OWNS byte-correctness — EmitRaw performs
// NO validation, marshalling, or timestamp stamping.
func (c *Client) EmitRaw(body []byte) {
	c.enqueue(body)
}

// Close stops accepting new events, drains the in-flight queue, and waits for the
// workers — bounded by ctx. Idempotent.
func (c *Client) Close(ctx context.Context) error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return nil
	}
	c.closed = true
	close(c.queue)
	c.closeMu.Unlock()

	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// marshal stamps an empty Timestamp (byte-parity with the reference client) then
// JSON-encodes. ev is taken by value so the caller's struct is never mutated.
func (c *Client) marshal(ev AuditEventCreate) ([]byte, error) {
	if ev.Timestamp == "" {
		ev.Timestamp = FormatTimestamp(c.now())
	}
	return json.Marshal(ev)
}

func (c *Client) startWorkers(n int) {
	for i := 0; i < n; i++ {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			for body := range c.queue {
				c.deliver(body)
			}
		}()
	}
}

// deliver runs on a background worker. It never lets a panic escape and uses a
// fresh Background context (the request that produced the event is already gone;
// the timeout comes from the http.Client).
func (c *Client) deliver(body []byte) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Warn("audit delivery panicked (swallowed)", "recover", r)
		}
	}()
	if err := c.post(context.Background(), body); err != nil {
		var ie *IngestError
		if errors.As(err, &ie) {
			c.log.Warn("audit service returned non-202 status", "status", ie.StatusCode, "error_id", ie.RequestID)
			return
		}
		c.log.Warn("audit logging failed", "error", err.Error())
	}
}

// enqueue is the non-blocking hand-off. On a full queue or after Close it DROPS
// and logs — the request path is never blocked.
func (c *Client) enqueue(body []byte) {
	c.closeMu.Lock()
	closed := c.closed
	c.closeMu.Unlock()
	if closed {
		n := c.dropped.Add(1)
		c.log.Warn("audit event dropped: client closed", "dropped_total", n)
		return
	}
	select {
	case c.queue <- body:
	default:
		n := c.dropped.Add(1)
		c.log.Warn("audit event dropped: queue full", "dropped_total", n)
	}
}

// post is the single transport primitive shared by the sync and async paths. It
// returns nil on 202, an *IngestError on any other status, or the transport error.
func (c *Client) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+ingestPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	// The audit service reads X-API-Key (NOT Authorization: Bearer) for
	// service-to-service ingest.
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body) // drain for connection reuse
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == statusAccepted {
		return nil
	}
	// NEVER read/echo the response body — it may echo request fields (R-A3-2).
	// Status + correlation id only.
	corr := resp.Header.Get("x-request-id")
	if corr == "" {
		corr = resp.Header.Get("x-amzn-requestid")
	}
	return &IngestError{StatusCode: resp.StatusCode, RequestID: corr}
}

func rstripSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// ---- Null-object emitter ----

// Noop is a no-op Emitter used when audit is not configured (audit URL / key
// unset). Wiring a Noop instead of nil lets call sites emit unconditionally.
type Noop struct{}

// NewNoop returns a no-op Emitter.
func NewNoop() Noop { return Noop{} }

func (Noop) LogEvent(context.Context, AuditEventCreate) error { return nil }
func (Noop) Emit(AuditEventCreate)                            {}
func (Noop) EmitRaw([]byte)                                   {}
func (Noop) Close(context.Context) error                      { return nil }

var _ Emitter = Noop{}
