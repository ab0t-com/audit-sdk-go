// Package auditclient is an isolated, typed Go client for the ab0t Audit Service
// (served at https://audit.service.ab0t.com / http://localhost:8004).
//
// # What it writes
//
// It targets Product 1 — the AI-agent / structured-platform event stream: an
// authenticated POST of an AuditEventCreate envelope to
//
//	POST {URL}/logs/ingest        (X-API-Key auth, success == 202 Accepted)
//
// These events land in the searchable `audit.audit_events` table (queryable via
// the audit service's /search/ and /logs/events surfaces). This client does NOT
// speak the Product-2 high-throughput /events/ingest surface — that is a
// different table with a different (raw-JSON) contract. See the ab0t Audit
// Service API reference for the full server surface.
//
// # Isolation
//
// This package is its own Go module (github.com/ab0t-com/audit-sdk-go) and
// depends ONLY on the standard library. It is meant to be embedded in any mesh
// service, so it brings no transitive dependencies with it. The transport is
// plain net/http.
//
// # Two ways to emit
//
// The Emitter port exposes both delivery modes:
//
//	LogEvent(ctx, ev)   — SYNCHRONOUS. Marshals, POSTs, and returns the outcome:
//	                      nil on 202, ErrMissingRequiredField on a bad envelope,
//	                      an *IngestError on a non-202, or the transport error.
//	                      Use it when you need to know the write happened (tests,
//	                      critical audit trails, a request that must not proceed
//	                      until the event is durably accepted).
//
//	Emit(ev)            — FIRE-AND-FORGET. Hands the event to a bounded background
//	                      worker queue and returns immediately. Nothing on this
//	                      path can block, slow, or fail the caller: a full queue
//	                      DROPS (counted + logged), a transport error / non-202 is
//	                      swallowed + logged (status + correlation id only, never
//	                      the response body), and a panic in delivery is recovered.
//	                      This is the mode for the hot request path — audit logging
//	                      must never take down user traffic.
//
//	EmitRaw(body)       — FIRE-AND-FORGET, like Emit, but takes ALREADY-MARSHALLED
//	                      JSON bytes and skips AuditEventCreate marshalling. For
//	                      callers whose on-wire body is byte-parity-locked to a shape
//	                      AuditEventCreate cannot reproduce (dict field order /
//	                      explicit nulls). The caller owns byte-correctness; the
//	                      transport (queue, POST, X-API-Key, no-redirect guard,
//	                      drop-on-saturation, panic recovery) is shared with Emit.
//
//	Close(ctx)          — Stops accepting new events, drains the in-flight queue
//	                      (bounded by ctx), and waits for the workers. Idempotent.
//
// # Wire contract
//
// The wire contract — /logs/ingest path, X-API-Key auth, 202 == success,
// no-redirect-follow so a 3xx can never forward the API key, body-less error
// logging, and the AuditEventCreate envelope (including the extra `timestamp`
// field the server ignores, kept for wire parity) — matches the ab0t Audit
// Service ingest API exactly.
//
// # Null-object
//
// Noop is a no-op Emitter for when audit is not configured (AUDIT_SERVICE_URL /
// API key unset). Wiring a Noop instead of nil lets call sites emit
// unconditionally without nil checks.
package auditclient
