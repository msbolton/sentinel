package registry

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestListeningTransportsNeedAPin(t *testing.T) {
	v := newValidator()
	f := &Feed{ID: "site", Name: "Site", Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":":4001"},"decoder":{"name":"sbs"}}`)}
	err := v.Validate(f)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Path != "/config/pinnedReplica" {
		t.Fatalf("expected a pinnedReplica problem, got %v", err)
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
