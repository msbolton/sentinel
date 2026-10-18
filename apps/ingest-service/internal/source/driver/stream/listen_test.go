package stream

import (
	"encoding/json"
	"testing"

	"github.com/sentinel/ingest-service/internal/source/driver"
	_ "github.com/sentinel/ingest-service/internal/source/transport/tcpclient"
	_ "github.com/sentinel/ingest-service/internal/source/transport/tcpserver"
	_ "github.com/sentinel/ingest-service/internal/source/transport/udp"
)

func TestStreamListen(t *testing.T) {
	for cfg, want := range map[string]struct {
		network string
		port    int
		ok      bool
	}{
		`{"transport":{"kind":"udp","address":":10110"}}`: {"udp", 10110, true},
		`{"transport":{"kind":"udp","address":":0"}}`:     {"udp", 0, true},
		// A listening transport always listens; an address with no numeric
		// port reports port 0 so the port rules refuse it.
		`{"transport":{"kind":"udp","address":":abc"}}`:      {"udp", 0, true},
		`{"transport":{"kind":"udp","address":":http"}}`:     {"udp", 0, true},
		`{"transport":{"kind":"udp","address":"localhost"}}`: {"udp", 0, true},
		// Unrelated mistyped fields do not hide a listener.
		`{"transport":{"kind":"udp","address":":6000","allow":"10.0.0.0/8"}}`: {"udp", 6000, true},
		`{"transport":{"kind":"tcpserver","address":""}}`:                     {"tcp", 0, true},
		`{"transport":{"kind":"tcpserver","address":"0.0.0.0:6000"}}`:         {"tcp", 6000, true},
		`{"transport":{"kind":"tcpserver","address":"127.0.0.1:0"}}`:          {"tcp", 0, true},
		`{"transport":{"kind":"tcpclient","address":"h:30003"}}`:              {"", 0, false},
		`{nope`: {"", 0, false},
	} {
		n, p, ok := driver.ListenOf(ID, json.RawMessage(cfg))
		if n != want.network || p != want.port || ok != want.ok {
			t.Errorf("%s: got %q %d %v", cfg, n, p, ok)
		}
	}
	if _, _, ok := driver.ListenOf("nope", nil); ok {
		t.Error("unknown driver")
	}
}
