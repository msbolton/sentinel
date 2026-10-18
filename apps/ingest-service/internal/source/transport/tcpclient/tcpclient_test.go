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

func TestReconnectsAndMessagesAreCounted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for i := 0; i < 2; i++ {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("one\ntwo\n"))
			c.Close() // drop the client: it must reconnect
		}
		ln.Close() // no third connection: the count stays at one reconnect
	}()
	tr, err := New(transport.Config{Kind: "tcpclient", Address: ln.Addr().String(), Framing: "line"})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return col.Count() == 4 }, 5*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}
	st := tr.State()
	if st.Detail["reconnects"] != int64(1) || st.Detail["messages"] != int64(4) || st.Detail["remote"] != ln.Addr().String() {
		t.Errorf("detail = %+v", st.Detail)
	}
}

func TestSinceIsSetWhileConnectedAndAbsentWhenDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		ln.Close() // the client cannot come back once dropped
		accepted <- c
	}()
	tr, err := New(transport.Config{Kind: "tcpclient", Address: ln.Addr().String(), Framing: "line"})
	if err != nil {
		t.Fatal(err)
	}
	stop := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { _, ok := tr.State().Detail["since"]; return ok }, 2*time.Second) {
		t.Fatalf("no since while connected: %+v", tr.State().Detail)
	}
	if s, _ := tr.State().Detail["since"].(string); s == "" {
		t.Errorf("since = %v", tr.State().Detail["since"])
	} else if _, err := time.Parse(time.RFC3339, s); err != nil {
		t.Errorf("since %q is not RFC 3339: %v", s, err)
	}
	if r := tr.State().Detail["reconnects"]; r != int64(0) {
		t.Errorf("reconnects after the first connection = %v, want 0", r)
	}
	(<-accepted).Close()
	if !transporttest.WaitFor(func() bool { _, ok := tr.State().Detail["since"]; return !ok }, 2*time.Second) {
		t.Errorf("since still reported after the connection dropped: %+v", tr.State().Detail)
	}
}
