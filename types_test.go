package auditclient

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestMarshal_Minimal proves a minimal (required-only) event marshals to exactly
// the three required keys — optional fields are omitted, not sent as null/empty.
func TestMarshal_Minimal(t *testing.T) {
	ev := AuditEventCreate{EventType: "user.login", Service: "auth-service", Action: "login"}
	got, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"event_type":"user.login","service":"auth-service","action":"login"}`
	if string(got) != want {
		t.Fatalf("minimal envelope byte mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// TestMarshal_FullEnvelope pins the byte shape of a fully-populated envelope:
// field names, JSON tags, declaration order, and that org_id/timestamp are in the
// body. This is the byte-exactness lock for the server AuditEventCreate contract.
func TestMarshal_FullEnvelope(t *testing.T) {
	ev := AuditEventCreate{
		EventType:    "tool.execution",
		Service:      "integration",
		Action:       "POST /chat.postMessage",
		Outcome:      "success",
		UserID:       "u_abc",
		OrgID:        "org_123",
		ResourceType: "tool",
		ResourceID:   "slack",
		SessionID:    "sess_xyz",
		IPAddress:    "10.0.0.1",
		UserAgent:    "curl/8",
		RequestData:  map[string]any{"method": "POST"},
		ResponseData: map[string]any{"status": float64(200)},
		Metadata:     map[string]any{"region": "ap-southeast-2"},
		Timestamp:    "2026-09-03T12:00:00",
	}
	got, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"event_type":"tool.execution","service":"integration","action":"POST /chat.postMessage",` +
		`"outcome":"success","user_id":"u_abc","org_id":"org_123","resource_type":"tool",` +
		`"resource_id":"slack","session_id":"sess_xyz","ip_address":"10.0.0.1","user_agent":"curl/8",` +
		`"request_data":{"method":"POST"},"response_data":{"status":200},` +
		`"metadata":{"region":"ap-southeast-2"},"timestamp":"2026-09-03T12:00:00"}`
	if string(got) != want {
		t.Fatalf("full envelope byte mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// TestMarshal_RoundTrip confirms every field survives a marshal/unmarshal cycle
// under the documented JSON keys (guards against a tag typo).
func TestMarshal_RoundTrip(t *testing.T) {
	in := AuditEventCreate{
		EventType: "e", Service: "s", Action: "a", Outcome: "failure",
		UserID: "u", OrgID: "o", ResourceType: "rt", ResourceID: "ri",
		SessionID: "sid", IPAddress: "1.2.3.4", UserAgent: "ua",
		RequestData: map[string]any{"k": "v"}, ResponseData: map[string]any{"n": float64(1)},
		Metadata: map[string]any{"m": "d"}, Timestamp: "2026-01-01T00:00:00",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out AuditEventCreate
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(out)
	if string(b) != string(b2) {
		t.Fatalf("round-trip drift:\n first=%s\nsecond=%s", b, b2)
	}
}

// TestValidate covers the required-field rule.
func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		ev   AuditEventCreate
		ok   bool
	}{
		{"all present", AuditEventCreate{EventType: "e", Service: "s", Action: "a"}, true},
		{"missing event_type", AuditEventCreate{Service: "s", Action: "a"}, false},
		{"missing service", AuditEventCreate{EventType: "e", Action: "a"}, false},
		{"missing action", AuditEventCreate{EventType: "e", Service: "s"}, false},
		{"empty", AuditEventCreate{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ev.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				if !errors.Is(err, ErrMissingRequiredField) {
					t.Fatalf("want ErrMissingRequiredField, got %v", err)
				}
			}
		})
	}
}

// TestFormatTimestamp pins the naive-UTC ISO format: no fraction when
// microseconds are 0, 6-digit fraction otherwise, no timezone suffix.
func TestFormatTimestamp(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"whole second", time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), "2026-09-03T12:00:00"},
		{"microseconds", time.Date(2026, 9, 3, 12, 0, 0, 123456000, time.UTC), "2026-09-03T12:00:00.123456"},
		{"non-utc converted", time.Date(2026, 9, 3, 22, 0, 0, 0, time.FixedZone("x", 10*3600)), "2026-09-03T12:00:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatTimestamp(tc.in); got != tc.want {
				t.Fatalf("FormatTimestamp = %q, want %q", got, tc.want)
			}
		})
	}
}
