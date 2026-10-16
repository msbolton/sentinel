package udp

import (
	"net"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

func TestDatagramsSplitIntoLines(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	tr, err := New(transport.Config{Kind: "udp", Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
		t.Fatal("listener not up")
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("!AIVDM,1\r\n!AIVDM,2\n"))
	_, _ = conn.Write([]byte("!AIVDM,3"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 3 }, 2*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}
	if p := col.Payloads(); p[0] != "!AIVDM,1" || p[2] != "!AIVDM,3" {
		t.Errorf("payloads = %v", p)
	}
}
