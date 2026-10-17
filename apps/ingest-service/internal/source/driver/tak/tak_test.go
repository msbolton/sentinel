package tak

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport/framing"
)

// fakeServer accepts one client at a time, records every <event> it
// receives and can send events to the client or drop it.
type fakeServer struct {
	ln       net.Listener
	mu       sync.Mutex
	conn     net.Conn
	received []string
	accepted chan struct{}
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, accepted: make(chan struct{}, 10)}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conn = conn
		s.mu.Unlock()
		s.accepted <- struct{}{}
		go s.read(conn)
	}
}

func (s *fakeServer) read(conn net.Conn) {
	split, _ := framing.New("xmlevent", 1<<20)
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	sc.Split(split)
	for sc.Scan() {
		s.mu.Lock()
		s.received = append(s.received, string(sc.Bytes()))
		s.mu.Unlock()
	}
}

func (s *fakeServer) events() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.received...)
}

func (s *fakeServer) waitFor(t *testing.T, substr string) string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		for _, e := range s.events() {
			if strings.Contains(e, substr) {
				return e
			}
		}
		select {
		case <-deadline:
			t.Fatalf("server never received %q; got %d events", substr, len(s.events()))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *fakeServer) send(t *testing.T, xml string) {
	t.Helper()
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		t.Fatal("no client connected")
	}
	if _, err := conn.Write([]byte(xml)); err != nil {
		t.Fatal(err)
	}
}

func (s *fakeServer) drop() {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// memSink records observations and skips.
type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	skipped []string
	dead    []string
}

func (m *memSink) Emit(_ context.Context, o *obs.Observation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.obs = append(m.obs, o)
	return nil
}
func (m *memSink) DeadLetter(_ []byte, reason string, _ error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dead = append(m.dead, reason)
}
func (m *memSink) Skipped(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skipped = append(m.skipped, reason)
}
func (m *memSink) Succeeded(int)         {}
func (m *memSink) Blocked(time.Duration) {}

func (m *memSink) wait(t *testing.T, n int) []*obs.Observation {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		m.mu.Lock()
		got := append([]*obs.Observation(nil), m.obs...)
		m.mu.Unlock()
		if len(got) >= n {
			return got
		}
		select {
		case <-deadline:
			t.Fatalf("got %d observations, want %d", len(got), n)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func startDriver(t *testing.T, srv *fakeServer, extra string) (*Driver, *memSink) {
	t.Helper()
	cfg := `{"address":"` + srv.ln.Addr().String() + `","siteId":"east","heartbeatSec":5,"shapes":{"enabled":false}` + extra + `}`
	d, err := New(driver.Spec{FeedID: "tak-test", Driver: ID, Config: json.RawMessage(cfg)}, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	drv := d.(*Driver)
	sink := &memSink{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- drv.Run(ctx, sink) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("driver did not stop")
		}
	})
	select {
	case <-srv.accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("driver never connected")
	}
	// The driver marks itself connected right after dialling; wait for it.
	deadline := time.After(time.Second)
	for !drv.Health().Connected {
		select {
		case <-deadline:
			t.Fatal("driver not connected")
		case <-time.After(5 * time.Millisecond):
		}
	}
	return drv, sink
}

func TestDriverPublishesAndRemoves(t *testing.T) {
	srv := newFakeServer(t)
	drv, _ := startDriver(t, srv, "")
	en := &sentinelv1.Entity{Id: "e1", Name: "UAL123", EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}}
	if err := drv.HandleEntity(context.Background(), driver.EntityUpdate{Kind: driver.UpsertEntity, EntityID: "e1", Entity: en}); err != nil {
		t.Fatal(err)
	}
	ev := srv.waitFor(t, `uid="SENTINEL-east-e1"`)
	if !strings.Contains(ev, `type="a-u-A-C-F"`) || !strings.Contains(ev, `callsign="UAL123"`) || !strings.Contains(ev, `<_sentinel site="east"`) {
		t.Errorf("track = %s", ev)
	}
	// Leaving the policy sends a final short-stale track; deletion sends t-x-d-d.
	if err := drv.HandleEntity(context.Background(), driver.EntityUpdate{Kind: driver.RemoveEntity, EntityID: "e1", Reason: "policy"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for len(srv.events()) < 2 {
		select {
		case <-deadline:
			t.Fatal("final track not received")
		case <-time.After(10 * time.Millisecond):
		}
	}
	_ = drv.HandleEntity(context.Background(), driver.EntityUpdate{Kind: driver.UpsertEntity, EntityID: "e1", Entity: en})
	_ = drv.HandleEntity(context.Background(), driver.EntityUpdate{Kind: driver.RemoveEntity, EntityID: "e1", Reason: "deleted"})
	del := srv.waitFor(t, `type="t-x-d-d"`)
	if !strings.Contains(del, `<link uid="SENTINEL-east-e1"`) || !strings.Contains(del, "__forcedelete") {
		t.Errorf("delete = %s", del)
	}
	// Removing an unknown entity sends nothing and is not an error.
	before := len(srv.events())
	if err := drv.HandleEntity(context.Background(), driver.EntityUpdate{Kind: driver.RemoveEntity, EntityID: "ghost", Reason: "deleted"}); err != nil || len(srv.events()) != before {
		t.Errorf("unknown removal: err=%v events=%d", err, len(srv.events()))
	}

	alert := &sentinelv1.AlertEvent{EventType: sentinelv1.AlertEventType_ALERT_EVENT_TYPE_TRIGGERED, Alert: &sentinelv1.Alert{AlertId: "a1", EntityId: "e1", Title: "Breach", Severity: sentinelv1.AlertSeverity_ALERT_SEVERITY_CRITICAL}}
	if err := drv.HandleAlert(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	if ev := srv.waitFor(t, `uid="SENTINEL-east-alert-a1"`); !strings.Contains(ev, `type="b-a-o-tbl"`) {
		t.Errorf("emergency = %s", ev)
	}
	resolved := proto.Clone(alert).(*sentinelv1.AlertEvent)
	resolved.EventType = sentinelv1.AlertEventType_ALERT_EVENT_TYPE_RESOLVED
	if err := drv.HandleAlert(context.Background(), resolved); err != nil {
		t.Fatal(err)
	}
	srv.waitFor(t, `type="b-a-o-can"`)
	if h := drv.Health(); !h.Connected || h.Detail["eventsSent"].(int64) < 6 {
		t.Errorf("health = %+v", h)
	}
}

func TestDriverDecodesInboundAndSkipsEchoes(t *testing.T) {
	srv := newFakeServer(t)
	drv, sink := startDriver(t, srv, "")
	pli := `<?xml version="1.0"?><event version="2.0" uid="ANDROID-123" type="a-f-G-U-C" time="2026-10-16T12:00:00Z" start="2026-10-16T12:00:00Z" stale="2026-10-16T12:05:00Z" how="m-g"><point lat="38.9" lon="-77.0" hae="100" ce="5" le="9999999"/><detail><contact callsign="ALPHA"/><track speed="1.5" course="90"/></detail></event>`
	echo := `<event version="2.0" uid="SENTINEL-east-e9" type="a-u-A-C-F" time="2026-10-16T12:00:00Z" start="2026-10-16T12:00:00Z" stale="2026-10-16T12:05:00Z" how="m-g"><point lat="1" lon="2" hae="9999999" ce="9999999" le="9999999"/><detail><contact callsign="X"/><_sentinel site="east" entity="e9"/></detail></event>`
	shape := `<event version="2.0" uid="SHAPE-1" type="u-d-f" time="2026-10-16T12:00:00Z" start="2026-10-16T12:00:00Z" stale="2026-10-16T12:05:00Z" how="h-e"><point lat="1" lon="2" hae="9999999" ce="9999999" le="9999999"/><detail/></event>`
	srv.send(t, pli+echo+shape+pli)
	got := sink.wait(t, 2)
	if got[0].SourceEntityID != "ANDROID-123" || got[0].DisplayName != "ALPHA" || !got[0].HasPosition || got[0].Latitude != 38.9 {
		t.Errorf("observation = %+v", got[0])
	}
	if len(got[0].Identities) == 0 || got[0].Identities[0].Type != obs.IdentityCoTUID {
		t.Errorf("identity = %+v", got[0].Identities)
	}
	sink.mu.Lock()
	skipped := strings.Join(sink.skipped, ",")
	sink.mu.Unlock()
	if !strings.Contains(skipped, "sentinel_echo") || !strings.Contains(skipped, "drawing_shape") {
		t.Errorf("skipped = %s", skipped)
	}
	if err := drv.HandleEntity(context.Background(), driver.EntityUpdate{Kind: driver.UpsertEntity, EntityID: "e1", Entity: &sentinelv1.Entity{Id: "e1", Position: &sentinelv1.Coordinate{}}}); err != nil {
		t.Fatal(err)
	}
}

func TestDriverReconnectsAndPausesDeliveries(t *testing.T) {
	srv := newFakeServer(t)
	drv, _ := startDriver(t, srv, `,"readTimeoutSec":1`)
	srv.drop()
	// Until the driver notices, writes fail into ErrNotConnected; then it
	// reconnects and deliveries resume.
	en := &sentinelv1.Entity{Id: "e1", Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}}
	var err error
	deadline := time.After(3 * time.Second)
	for {
		err = drv.HandleEntity(context.Background(), driver.EntityUpdate{Kind: driver.UpsertEntity, EntityID: "e1", Entity: en})
		if errors.Is(err, driver.ErrNotConnected) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("writes kept succeeding after the drop: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	select {
	case <-srv.accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("driver did not reconnect")
	}
	deadline = time.After(3 * time.Second)
	for {
		if err := drv.HandleEntity(context.Background(), driver.EntityUpdate{Kind: driver.UpsertEntity, EntityID: "e1", Entity: en}); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("deliveries did not resume")
		case <-time.After(20 * time.Millisecond):
		}
	}
	srv.waitFor(t, `uid="SENTINEL-east-e1"`)
	if h := drv.Health(); h.Detail["connects"].(int64) < 2 {
		t.Errorf("connects = %v", h.Detail["connects"])
	}
}

func TestNewValidatesConfigAndTLS(t *testing.T) {
	if _, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(`{}`)}, driver.Deps{}); err == nil {
		t.Error("missing address accepted")
	}
	if _, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(`{"address":"nohost"}`)}, driver.Deps{}); err == nil {
		t.Error("bad address accepted")
	}
	if _, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(`{"address":"h:1","protocol":"protobuf"}`)}, driver.Deps{}); err == nil {
		t.Error("unsupported protocol accepted")
	}
	if _, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(`{"address":"h:1","tls":{"enabled":true}}`), Secrets: map[string]string{"clientCert": "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----"}}, driver.Deps{}); err == nil {
		t.Error("cert without key accepted")
	}
	if _, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(`{"address":"h:1","tls":{"enabled":true}}`), Secrets: map[string]string{"caCert": "not a cert"}}, driver.Deps{}); err == nil {
		t.Error("bad CA accepted")
	}
	d, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(`{"address":"h:1","tls":{"enabled":true,"serverName":"tak"}}`)}, driver.Deps{})
	if err != nil || d.(*Driver).tls == nil || d.(*Driver).tls.ServerName != "tak" {
		t.Errorf("tls without client cert: %v", err)
	}
}
