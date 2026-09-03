package auditclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var fixedTime = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// capturedReq is one recorded ingest request.
type capturedReq struct {
	method      string
	path        string
	apiKey      string
	contentType string
	userAgent   string
	body        []byte
}

// recordingServer is an httptest server that records every request and answers
// with the configured status. Requests are delivered over a buffered channel so
// handlers never block and the -race detector stays happy.
type recordingServer struct {
	*httptest.Server
	status int
	reqs   chan capturedReq
}

func newRecordingServer(t *testing.T, status int) *recordingServer {
	t.Helper()
	rs := &recordingServer{status: status, reqs: make(chan capturedReq, 256)}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rs.reqs <- capturedReq{
			method:      r.Method,
			path:        r.URL.Path,
			apiKey:      r.Header.Get("X-API-Key"),
			contentType: r.Header.Get("Content-Type"),
			userAgent:   r.Header.Get("User-Agent"),
			body:        body,
		}
		w.Header().Set("x-request-id", "corr-123")
		w.WriteHeader(rs.status)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingServer) wait(t *testing.T) capturedReq {
	t.Helper()
	select {
	case r := <-rs.reqs:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ingest request")
		return capturedReq{}
	}
}

func sampleEvent() AuditEventCreate {
	return AuditEventCreate{
		EventType: "tool.execution", Service: "integration", Action: "POST /x",
		Outcome: "success", OrgID: "org_123", UserID: "u_abc", ResourceType: "tool", ResourceID: "slack",
	}
}

// TestLogEvent_Success asserts the full sync path: correct method+path, X-API-Key,
// Content-Type, the byte-exact envelope (org_id in body, stamped timestamp), 202 -> nil.
func TestLogEvent_Success(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	c := New(Config{URL: rs.URL, APIKey: "ab0t_sk_test", Logger: quietLogger(), Now: func() time.Time { return fixedTime }})
	defer c.Close(context.Background())

	if err := c.LogEvent(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}
	req := rs.wait(t)

	if req.method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.method)
	}
	if req.path != "/logs/ingest" {
		t.Errorf("path = %q, want /logs/ingest", req.path)
	}
	if req.apiKey != "ab0t_sk_test" {
		t.Errorf("X-API-Key = %q", req.apiKey)
	}
	if req.contentType != "application/json" {
		t.Errorf("Content-Type = %q", req.contentType)
	}
	if !strings.HasPrefix(req.userAgent, "ab0t-audit-sdk-go/") {
		t.Errorf("User-Agent = %q", req.userAgent)
	}
	want := `{"event_type":"tool.execution","service":"integration","action":"POST /x",` +
		`"outcome":"success","user_id":"u_abc","org_id":"org_123","resource_type":"tool",` +
		`"resource_id":"slack","timestamp":"2026-09-03T12:00:00"}`
	if string(req.body) != want {
		t.Fatalf("body byte mismatch:\n got=%s\nwant=%s", req.body, want)
	}
}

// TestLogEvent_Non202 asserts a non-202 becomes an *IngestError carrying status +
// correlation id and NEVER the response body.
func TestLogEvent_Non202(t *testing.T) {
	rs := newRecordingServer(t, http.StatusInternalServerError)
	// Server writes a body that must not leak into the error.
	rs.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		rs.reqs <- capturedReq{path: r.URL.Path}
		w.Header().Set("x-request-id", "corr-err")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"secret_echo":"do-not-leak"}`))
	})
	c := New(Config{URL: rs.URL, APIKey: "k", Logger: quietLogger()})
	defer c.Close(context.Background())

	err := c.LogEvent(context.Background(), sampleEvent())
	rs.wait(t)
	if err == nil {
		t.Fatal("want error on non-202")
	}
	var ie *IngestError
	if !errors.As(err, &ie) {
		t.Fatalf("want *IngestError, got %T: %v", err, err)
	}
	if ie.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d", ie.StatusCode)
	}
	if ie.RequestID != "corr-err" {
		t.Errorf("request_id = %q", ie.RequestID)
	}
	if strings.Contains(err.Error(), "do-not-leak") {
		t.Fatal("response body leaked into error string")
	}
}

// TestLogEvent_InvalidEnvelope asserts a bad envelope fails fast with no HTTP call.
func TestLogEvent_InvalidEnvelope(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	c := New(Config{URL: rs.URL, APIKey: "k", Logger: quietLogger()})
	defer c.Close(context.Background())

	err := c.LogEvent(context.Background(), AuditEventCreate{EventType: "e"}) // no service/action
	if !errors.Is(err, ErrMissingRequiredField) {
		t.Fatalf("want ErrMissingRequiredField, got %v", err)
	}
	select {
	case <-rs.reqs:
		t.Fatal("invalid envelope must not hit the network")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestLogEvent_MarshalError asserts an unmarshalable metadata value returns an error
// (and, on the Emit path, is swallowed) without a network call.
func TestLogEvent_MarshalError(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	c := New(Config{URL: rs.URL, APIKey: "k", Logger: quietLogger()})
	defer c.Close(context.Background())

	bad := sampleEvent()
	bad.Metadata = map[string]any{"ch": make(chan int)} // channels are not JSON-marshalable
	if err := c.LogEvent(context.Background(), bad); err == nil {
		t.Fatal("want marshal error")
	}
	c.Emit(bad) // must not panic / must be swallowed
	select {
	case <-rs.reqs:
		t.Fatal("marshal-failed event must not hit the network")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestNoRedirectFollow proves a 3xx is NOT followed, so the X-API-Key is never
// resent to the redirect target.
func TestNoRedirectFollow(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		if r.Header.Get("X-API-Key") != "" {
			t.Errorf("API key forwarded to redirect target: %q", r.Header.Get("X-API-Key"))
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/logs/ingest", http.StatusTemporaryRedirect)
	}))
	defer front.Close()

	c := New(Config{URL: front.URL, APIKey: "ab0t_sk_secret", Logger: quietLogger()})
	defer c.Close(context.Background())

	err := c.LogEvent(context.Background(), sampleEvent())
	var ie *IngestError
	if !errors.As(err, &ie) || ie.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("want IngestError with 307 (redirect not followed), got %v", err)
	}
	if n := atomic.LoadInt32(&targetHits); n != 0 {
		t.Fatalf("redirect target was hit %d times — redirect was followed", n)
	}
}

// TestEmit_DeliversAndDrains proves the fire-and-forget path reaches /logs/ingest
// and that Close drains it.
func TestEmit_DeliversAndDrains(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	c := New(Config{URL: rs.URL, APIKey: "k", Workers: 2, Logger: quietLogger(), Now: func() time.Time { return fixedTime }})

	c.Emit(sampleEvent())
	req := rs.wait(t)
	if req.path != "/logs/ingest" {
		t.Errorf("path = %q", req.path)
	}
	var got AuditEventCreate
	if err := json.Unmarshal(req.body, &got); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if got.EventType != "tool.execution" || got.OrgID != "org_123" {
		t.Errorf("unexpected body: %+v", got)
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if c.Dropped() != 0 {
		t.Errorf("unexpected drops: %d", c.Dropped())
	}
}

// TestEmit_QueueFullDrops proves a saturated queue drops (non-blocking) instead of
// blocking the caller.
func TestEmit_QueueFullDrops(t *testing.T) {
	release := make(chan struct{})
	blocking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // stall the single worker so the queue fills
		w.WriteHeader(http.StatusAccepted)
	}))
	defer blocking.Close()

	c := New(Config{URL: blocking.URL, APIKey: "k", Workers: 1, QueueSize: 1, Logger: quietLogger()})

	for i := 0; i < 50; i++ {
		c.Emit(sampleEvent()) // must never block
	}
	if c.Dropped() == 0 {
		t.Fatal("expected drops under saturation, got 0")
	}
	close(release)
	_ = c.Close(context.Background())
}

// TestEmit_NonBlocking proves Emit returns promptly even while the worker is
// stalled mid-delivery.
func TestEmit_NonBlocking(t *testing.T) {
	release := make(chan struct{})
	blocking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	defer blocking.Close()
	c := New(Config{URL: blocking.URL, APIKey: "k", Workers: 1, QueueSize: 4, Logger: quietLogger()})

	c.Emit(sampleEvent()) // occupies the worker
	done := make(chan struct{})
	go func() {
		c.Emit(sampleEvent())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Emit blocked while worker was busy — fire-and-forget violated")
	}
	close(release)
	_ = c.Close(context.Background())
}

// TestClose_IdempotentAndDropsAfter proves Close is idempotent and post-close Emit drops.
func TestClose_IdempotentAndDropsAfter(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	c := New(Config{URL: rs.URL, APIKey: "k", Logger: quietLogger()})

	c.Emit(sampleEvent())
	rs.wait(t)

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("second close must be nil: %v", err)
	}
	before := c.Dropped()
	c.Emit(sampleEvent())
	if c.Dropped() != before+1 {
		t.Errorf("post-close Emit should drop; before=%d after=%d", before, c.Dropped())
	}
}

// TestClose_RespectsDeadline proves Close returns ctx.Err() if the drain can't finish.
func TestClose_RespectsDeadline(t *testing.T) {
	release := make(chan struct{})
	blocking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	defer blocking.Close()
	c := New(Config{URL: blocking.URL, APIKey: "k", Workers: 1, QueueSize: 8, Logger: quietLogger()})

	c.Emit(sampleEvent()) // worker will stall on this delivery
	// Give the worker a moment to pick the job up so Close must wait on it.
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Close(ctx); err == nil {
		t.Fatal("expected deadline error while worker blocked")
	}

	// Unblock so the worker, the drain, and the deferred server Close can all
	// finish cleanly (httptest.Server.Close waits for the in-flight request).
	close(release)
	_ = c.Close(context.Background())
}

// TestNoop_NoOps proves the null-object emitter neither errors nor hits the network.
func TestNoop_NoOps(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	var n Emitter = NewNoop()

	if err := n.LogEvent(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Noop.LogEvent = %v, want nil", err)
	}
	n.Emit(sampleEvent())
	if err := n.Close(context.Background()); err != nil {
		t.Fatalf("Noop.Close = %v, want nil", err)
	}
	select {
	case <-rs.reqs:
		t.Fatal("Noop must not hit the network")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestOptions_OverrideConfig proves New applies Option funcs on top of the Config.
func TestOptions_OverrideConfig(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	c := New(Config{URL: "http://replaced.invalid/"},
		WithURL(rs.URL+"/"), WithAPIKey("opt-key"), WithLogger(quietLogger()), WithUserAgent("custom-ua/9"))
	defer c.Close(context.Background())

	if c.BaseURL() != rs.URL {
		t.Errorf("BaseURL = %q, want %q (trailing slash trimmed, option-overridden)", c.BaseURL(), rs.URL)
	}
	if err := c.LogEvent(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}
	req := rs.wait(t)
	if req.apiKey != "opt-key" {
		t.Errorf("X-API-Key = %q, want opt-key", req.apiKey)
	}
	if req.userAgent != "custom-ua/9" {
		t.Errorf("User-Agent = %q, want custom-ua/9", req.userAgent)
	}
}
