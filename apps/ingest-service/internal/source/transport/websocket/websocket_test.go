package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

func TestReceivesMessagesAndReconnects(t *testing.T) {
	up := gws.Upgrader{}
	conns := make(chan *gws.Conn, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conns <- c
	}))
	defer srv.Close()
	tr, err := New(transport.Config{Kind: "websocket", URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	var c *gws.Conn
	select {
	case c = <-conns:
	case <-time.After(2 * time.Second):
		t.Fatal("no connection")
	}
	_ = c.WriteMessage(gws.TextMessage, []byte(`{"a":1}`))
	_ = c.WriteMessage(gws.BinaryMessage, []byte{1, 2})
	if !transporttest.WaitFor(func() bool { return col.Count() == 2 }, 2*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}
	c.Close()
	select {
	case c = <-conns:
	case <-time.After(5 * time.Second):
		t.Fatal("no reconnect")
	}
	_ = c.WriteMessage(gws.TextMessage, []byte("x"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 3 }, 2*time.Second) {
		t.Fatalf("frames after reconnect = %v", col.Payloads())
	}
	c.Close()
	for _, bad := range []transport.Config{{Kind: "websocket"}, {Kind: "websocket", URL: "http://x"}, {Kind: "websocket", URL: "ws://x", Framing: "line"}} {
		if _, err := New(bad); err == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
}
