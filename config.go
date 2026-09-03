package auditclient

import (
	"log/slog"
	"net/http"
	"time"
)

// Config configures a Client. Only URL is required for a live emitter (APIKey is
// required for the audit service to accept the write, but an empty key is allowed
// so tests / local stacks can run). Everything else has a sensible default.
//
// Fields can be set directly (Config-literal style) and/or via
// the Option funcs passed to New — options are applied on top of the struct.
type Config struct {
	// URL is the audit service base URL, e.g. "https://audit.service.ab0t.com" or
	// "http://localhost:8004". Trailing slashes are trimmed. Required.
	URL string
	// APIKey is the service's audit API key, sent as the X-API-Key header.
	APIKey string
	// Timeout bounds each POST. Defaults to 5s.
	Timeout time.Duration
	// QueueSize bounds the fire-and-forget backlog. Defaults to 1024. On overflow,
	// Emit DROPS the event (counted via Dropped, logged) rather than blocking.
	QueueSize int
	// Workers is the number of background delivery goroutines. Defaults to 4.
	Workers int
	// HTTPClient overrides the transport. When nil, the client builds one with
	// Timeout and — critically — CheckRedirect set to NOT follow redirects, so a
	// 3xx can never forward the X-API-Key. If you supply your own client, set the
	// same CheckRedirect policy to preserve that guarantee.
	HTTPClient *http.Client
	// Logger receives the swallowed-error / dropped-event lines. Defaults to
	// slog.Default().
	Logger *slog.Logger
	// Now overrides the clock (used to stamp an empty AuditEventCreate.Timestamp).
	// Defaults to time.Now.
	Now func() time.Time
	// UserAgent overrides the User-Agent header. Defaults to
	// "ab0t-audit-sdk-go/<Version>".
	UserAgent string
}

// Option mutates a Config. Options mirror auth-sdk-go's WithX style and are
// applied by New after the base Config literal, so they override struct fields.
type Option func(*Config)

// WithURL sets the audit service base URL.
func WithURL(u string) Option { return func(c *Config) { c.URL = u } }

// WithAPIKey sets the X-API-Key credential.
func WithAPIKey(key string) Option { return func(c *Config) { c.APIKey = key } }

// WithTimeout sets the per-POST timeout. A non-positive value keeps the default.
func WithTimeout(d time.Duration) Option { return func(c *Config) { c.Timeout = d } }

// WithQueueSize bounds the fire-and-forget backlog. A non-positive value keeps
// the default (1024).
func WithQueueSize(n int) Option { return func(c *Config) { c.QueueSize = n } }

// WithWorkers sets the number of background delivery goroutines. A non-positive
// value keeps the default (4).
func WithWorkers(n int) Option { return func(c *Config) { c.Workers = n } }

// WithHTTPClient supplies a custom *http.Client. See Config.HTTPClient for the
// redirect-policy caveat.
func WithHTTPClient(h *http.Client) Option { return func(c *Config) { c.HTTPClient = h } }

// WithLogger sets the logger for swallowed errors and dropped events.
func WithLogger(l *slog.Logger) Option { return func(c *Config) { c.Logger = l } }

// WithNow overrides the clock (tests / timestamp stamping).
func WithNow(now func() time.Time) Option { return func(c *Config) { c.Now = now } }

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Config) { c.UserAgent = ua } }
