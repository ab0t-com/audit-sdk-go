package auditclient

import (
	"errors"
	"fmt"
	"time"
)

// AuditEventCreate is the wire envelope POSTed to /logs/ingest — byte-compatible
// with the audit service's `AuditEventCreate` model (Product 1).
//
// Contract notes (see the ab0t Audit Service API reference):
//   - event_type, service and action are REQUIRED. Everything else is optional.
//   - outcome is conventionally one of "success", "failure", "error", "blocked";
//     the server defaults it to "success" when omitted.
//   - org_id travels in the BODY. When omitted, the audit service defaults it from
//     the caller's API key / token org claim; a value that conflicts with the
//     caller's org is rejected 403 unless the caller holds audit.cross_tenant.
//   - request_data / response_data / metadata are free-form JSON objects (stored
//     as JSON strings in ClickHouse). metadata defaults to {} server-side.
//   - timestamp is NOT part of the documented server schema — the service's model
//     ignores unknown fields. It is retained here for byte-parity with the
//     reference audit clients, which always send it. If left empty, the client
//     stamps it (naive-UTC ISO format) at emit time using the configured clock;
//     set it yourself to override.
//
// JSON field ORDER follows the server's schema documentation; order is not
// wire-significant (the service reparses the body). Optional fields use omitempty
// so a minimal event marshals to just the three required keys.
type AuditEventCreate struct {
	// --- required ---
	EventType string `json:"event_type"`
	Service   string `json:"service"`
	Action    string `json:"action"`

	// --- optional ---
	Outcome string `json:"outcome,omitempty"`
	// UserID is the SUBJECT the event is about — NOT necessarily the stored actor.
	// The audit service binds the event's actor from the AUTHENTICATED credential's
	// principal, never verbatim from this field: a caller-supplied user_id is only
	// honored as the actor when the caller holds the delegation or cross-tenant
	// permission; otherwise it is overridden with the credential's own user, and the
	// write-time hash chain seals that binding. So do NOT expect to attribute an
	// event to an arbitrary user by setting UserID — the service rejects that as a
	// forgery.
	UserID       string         `json:"user_id,omitempty"`
	OrgID        string         `json:"org_id,omitempty"`
	ResourceType string         `json:"resource_type,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	SessionID    string         `json:"session_id,omitempty"`
	IPAddress    string         `json:"ip_address,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	RequestData  map[string]any `json:"request_data,omitempty"`
	ResponseData map[string]any `json:"response_data,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`

	// timestamp is ignored by the server (kept for wire parity). See the type doc.
	Timestamp string `json:"timestamp,omitempty"`
}

// ErrMissingRequiredField is returned by Validate / LogEvent when event_type,
// service or action is empty.
var ErrMissingRequiredField = errors.New("auditclient: event_type, service and action are required")

// Validate checks the three required fields are present. It performs no network
// I/O. LogEvent calls it before marshalling; Emit calls it before enqueueing and
// drops (logged) if it fails.
func (ev AuditEventCreate) Validate() error {
	if ev.EventType == "" || ev.Service == "" || ev.Action == "" {
		return ErrMissingRequiredField
	}
	return nil
}

// FormatTimestamp renders t as a NAIVE UTC ISO-8601 timestamp (no timezone
// suffix), matching the audit service's expected timestamp shape:
//
//	"2006-01-02T15:04:05"          when the microsecond component is 0
//	"2006-01-02T15:04:05.000000"   otherwise (6-digit fraction, no timezone suffix)
//
// Kept for byte-parity with the reference clients. The server ignores the value.
func FormatTimestamp(t time.Time) string {
	u := t.UTC()
	base := u.Format("2006-01-02T15:04:05")
	if us := u.Nanosecond() / 1000; us != 0 {
		return fmt.Sprintf("%s.%06d", base, us)
	}
	return base
}
