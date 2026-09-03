package auditclient

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestEmitRaw_DeliversExactBytes proves EmitRaw POSTs the caller-supplied body
// byte-for-byte (no re-marshalling, no timestamp stamping) to /logs/ingest with the
// X-API-Key — the seam a caller relies on to keep a byte-parity-locked wire body.
func TestEmitRaw_DeliversExactBytes(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	// A body whose field ORDER + explicit nulls AuditEventCreate could never produce.
	raw := []byte(`{"event_type":"tool.execution","service":"integration","action":"POST /x",` +
		`"org_id":"o","user_id":"u","resource_type":"tool","resource_id":"slack","outcome":"success",` +
		`"metadata":{"connection_id":"c","service_id":"slack","request_size_bytes":null},` +
		`"ip_address":null,"user_agent":null,"timestamp":"2026-09-03T12:00:00"}`)

	c := New(Config{URL: rs.URL, APIKey: "raw-key", Logger: quietLogger(), Now: func() time.Time { return fixedTime }})
	defer c.Close(context.Background())

	c.EmitRaw(raw)
	req := rs.wait(t)

	if req.path != "/logs/ingest" {
		t.Errorf("path = %q, want /logs/ingest", req.path)
	}
	if req.apiKey != "raw-key" {
		t.Errorf("X-API-Key = %q", req.apiKey)
	}
	if string(req.body) != string(raw) {
		t.Fatalf("EmitRaw body must be byte-identical:\n got=%s\nwant=%s", req.body, raw)
	}
}

// TestEmitRaw_DropsAfterClose proves EmitRaw shares Emit's fire-and-forget close
// semantics: after Close it drops (counted), never blocks, never errors.
func TestEmitRaw_DropsAfterClose(t *testing.T) {
	rs := newRecordingServer(t, http.StatusAccepted)
	c := New(Config{URL: rs.URL, APIKey: "k", Logger: quietLogger()})
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	before := c.Dropped()
	c.EmitRaw([]byte(`{"event_type":"e","service":"s","action":"a"}`))
	if c.Dropped() != before+1 {
		t.Errorf("post-close EmitRaw should drop; before=%d after=%d", before, c.Dropped())
	}
}

// TestNoop_EmitRaw proves the null-object emitter's EmitRaw is a safe no-op.
func TestNoop_EmitRaw(t *testing.T) {
	var e Emitter = NewNoop()
	e.EmitRaw([]byte(`{"anything":true}`)) // must not panic / must not hit the network
}
