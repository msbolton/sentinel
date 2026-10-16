package tcpserver

import (
	"net"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

func TestAcceptsConnectionsAndFrames(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line"})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()

	var c1, c2 net.Conn
	if !transporttest.WaitFor(func() bool {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c1 = c
		}
		return err == nil
	}, 2*time.Second) {
		t.Fatal("listener not up")
	}
	defer c1.Close()
	c2, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	_, _ = c1.Write([]byte("one\r\ntwo\n"))
	_, _ = c2.Write([]byte("three\n"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 3 }, 2*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}
	if st := tr.State(); !st.Connected || st.Connections != 2 {
		t.Errorf("state = %+v", st)
	}
	for _, f := range col.Frames() {
		if f.Remote == "" || f.ReceivedAt.IsZero() {
			t.Errorf("frame lacks provenance: %+v", f)
		}
	}
	c1.Close()
	if !transporttest.WaitFor(func() bool { return tr.State().Connections == 1 }, 2*time.Second) {
		t.Errorf("connection close not tracked: %+v", tr.State())
	}
}

func TestBindFailureIsFatal(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tr, err := New(transport.Config{Kind: "tcpserver", Address: l.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Run(t.Context(), (&transporttest.Collector{}).Emit); err == nil {
		t.Error("expected a bind error")
	}
	if _, err := New(transport.Config{Kind: "tcpserver", Address: "nope"}); err == nil {
		t.Error("expected a validation error")
	}
}
