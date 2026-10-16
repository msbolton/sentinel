package tcpclient

import (
	"net"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

func TestConnectsAndReconnects(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	tr, err := New(transport.Config{Kind: "tcpclient", Address: ln.Addr().String(), Framing: "line"})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()

	var c net.Conn
	select {
	case c = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not connect")
	}
	_, _ = c.Write([]byte("hello\nworld\n"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 2 }, 2*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}
	if !tr.State().Connected {
		t.Error("should report connected")
	}
	c.Close() // server drops the connection: the client reconnects
	select {
	case c = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("client did not reconnect")
	}
	_, _ = c.Write([]byte("again\n"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 3 }, 2*time.Second) {
		t.Fatalf("frames after reconnect = %v", col.Payloads())
	}
	c.Close()
}

func TestUnreachableKeepsRetrying(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpclient", Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	stop := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
	if !transporttest.WaitFor(func() bool { return tr.State().LastError != "" }, 2*time.Second) {
		t.Error("connection failure not reported")
	}
	stop()
}
