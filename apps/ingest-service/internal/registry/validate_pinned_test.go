package registry

import (
	"encoding/json"
	"testing"
)

// Listening feeds are placed on a listener replica, so pinnedReplica is
// optional for them; an explicit pin is still accepted.
func TestListeningTransportsNeedNoPin(t *testing.T) {
	v := newValidator()
	f := &Feed{ID: "site", Name: "Site", Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":":4001"},"decoder":{"name":"sbs"}}`)}
	if err := v.Validate(f); err != nil {
		t.Fatalf("a listening transport needs no pin: %v", err)
	}
	f.Config = json.RawMessage(`{"transport":{"kind":"tcpserver","address":":4001"},"decoder":{"name":"sbs"},"pinnedReplica":"ingest-0"}`)
	if err := v.Validate(f); err != nil {
		t.Fatal(err)
	}
	f.Config = json.RawMessage(`{"transport":{"kind":"tcpclient","address":"readsb:30003"},"decoder":{"name":"sbs"}}`)
	if err := v.Validate(f); err != nil {
		t.Fatalf("a client transport needs no pin: %v", err)
	}
}
