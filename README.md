# ab0t Audit Service — Go SDK

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A tiny Go client for writing structured events to the [ab0t Audit Service](https://audit.service.ab0t.com)
Product-1 ingest surface (`POST /logs/ingest`, the searchable `audit.audit_events` stream).

- **Standard library only.** No `require` block, no transitive dependencies. This module is meant to
  be embedded in any mesh service's binary, so it brings nothing with it. CI enforces this.
- **Interface-first.** `Emitter` is a three-method interface (`LogEvent` / `Emit` / `Close`), so your
  call sites are testable against `Noop` (or your own fake) with no live audit service.
- **Fire-and-forget is truly fire-and-forget.** The async path can never block, slow, or fail user
  traffic: a full queue drops (counted + logged), transport errors / non-202s are swallowed + logged
  (status + correlation id only — never the response body), and a panic in delivery is recovered.
- **Credential-safe transport.** The default HTTP client does **not** follow redirects, so a 3xx can
  never forward your `X-API-Key` to a redirect target.

```bash
go get github.com/ab0t-com/audit-sdk-go
```

Requires Go 1.23+.

## Two ways to emit

```go
import audit "github.com/ab0t-com/audit-sdk-go"

c := audit.New(audit.Config{
    URL:    os.Getenv("AUDIT_SERVICE_URL"),      // e.g. https://audit.service.ab0t.com
    APIKey: os.Getenv("AUDIT_SERVICE_API_KEY"),  // sent as X-API-Key
})
defer c.Close(context.Background())              // drains the background queue
```

### Fire-and-forget (the hot request path)

```go
c.Emit(audit.AuditEventCreate{
    EventType: "tool.execution",
    Service:   "integration",
    Action:    "POST /chat.postMessage",
    Outcome:   "success",
    OrgID:     orgID,   // travels in the body; defaults from the caller's key server-side if empty
    UserID:    userID,
    Metadata:  map[string]any{"service_id": "slack"},
})
// returns immediately — nothing here can fail your request
```

### Synchronous (when you need to know it landed)

```go
if err := c.LogEvent(ctx, ev); err != nil {
    // ErrMissingRequiredField, an *audit.IngestError (non-202), or a transport error
}
```

`LogEvent` returns `nil` only on `202 Accepted`.

### Options

`New` takes a `Config` literal and/or option funcs (applied on top of the struct), mirroring the
sibling `auth-sdk-go` style:

```go
c := audit.New(audit.Config{URL: url},
    audit.WithAPIKey(key),
    audit.WithQueueSize(4096),
    audit.WithWorkers(8),
    audit.WithTimeout(3*time.Second),
    audit.WithHTTPClient(myClient),   // keep CheckRedirect = no-follow if you override
)
```

### Null-object

When audit isn't configured, wire a `Noop` instead of `nil` so call sites emit unconditionally:

```go
var emitter audit.Emitter = audit.NewNoop()
```

## The wire contract

This client targets **Product 1** (AI-agent / structured platform events — searchable via the audit
service's `/search/` and `/logs/events`), not the Product-2 high-throughput `/events/ingest` surface.

| Aspect | Value |
|---|---|
| Endpoint | `POST {URL}/logs/ingest` |
| Auth | `X-API-Key: <key>` header |
| Success | `202 Accepted` (anything else → `*IngestError`) |
| Body | `AuditEventCreate` JSON (see `types.go`); `org_id` in the body |
| Redirects | not followed (a 3xx never re-sends the key) |
| Error logging | status + correlation id only, never the response body |

`AuditEventCreate` requires `event_type`, `service` and `action`; everything else is optional.
Optional fields use `omitempty` (a minimal event marshals to just the three required keys). The
envelope also carries a `timestamp` field that is **not** part of the documented server schema — the
service ignores unknown fields, and it is retained for byte-parity with the reference audit clients.
If you leave it empty, the client stamps it (naive-UTC ISO format) at emit time.

See the ab0t Audit Service API reference for the full server surface (batch, stream, read, search,
compliance, export, admin).

## Contract

This client targets the ab0t Audit Service ingest API (Product 1 — the structured-platform event
stream). The wire behavior — `/logs/ingest`, `X-API-Key` auth, `202 Accepted` == success,
no-redirect-follow, body-less error logging, and the `AuditEventCreate` envelope — matches that API
exactly.

## Development

```bash
make check   # gofmt -l, go vet, go test -race, and the stdlib-only assertion
```
