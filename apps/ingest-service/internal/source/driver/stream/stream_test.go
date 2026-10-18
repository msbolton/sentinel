package stream

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"

	_ "github.com/sentinel/ingest-service/internal/source/decoder/sbs"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/sentinelobs"
	_ "github.com/sentinel/ingest-service/internal/source/transport/tcpserver"
)

type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	dlq     []string
	skipped map[string]int
	success int
	blocked time.Duration
	slow    chan struct{} // when non-nil, Emit blocks until closed
}

func newSink() *memSink { return &memSink{skipped: map[string]int{}} }

func (s *memSink) Emit(ctx context.Context, o *obs.Observation) error {
	if s.slow != nil {
		select {
		case <-s.slow:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, o)
	return nil
}
func (s *memSink) DeadLetter(raw []byte, reason string, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dlq = append(s.dlq, reason+":"+string(raw))
}
func (s *memSink) Skipped(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipped[reason]++
}
func (s *memSink) Succeeded(int)           { s.mu.Lock(); s.success++; s.mu.Unlock() }
func (s *memSink) Blocked(d time.Duration) { s.mu.Lock(); s.blocked += d; s.mu.Unlock() }
func (s *memSink) count() int              { s.mu.Lock(); defer s.mu.Unlock(); return len(s.obs) }
func (s *memSink) deadLetters() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.dlq...)
}
func (s *memSink) skips(reason string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped[reason]
}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestTCPServerWithSBSEndToEnd(t *testing.T) {
	addr := freePort(t)
	spec := driver.Spec{FeedID: "site-1", Driver: ID, Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":"` + addr + `"},"framing":"line","decoder":{"name":"sbs"}}`)}
	d, err := New(spec, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	sink := newSink()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, sink) }()

	var conn net.Conn
	if !waitFor(func() bool {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			conn = c
		}
		return err == nil
	}, 2*time.Second) {
		t.Fatal("server did not come up")
	}
	defer conn.Close()
	lines := "MSG,3,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,BAW123,35000,450,180.5,51.5074,-0.1278,,,,,,0\r\n" +
		"   \n" + // blank line: no crash, dead-lettered
		"MSG,8,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,,,,,,,,,,,0\n" + // skipped
		"not,an,sbs\n" + // dead-lettered
		"MSG,3,1,1,A1B2C3,1,2025/01/15,12:30:01.000,2025/01/15,12:30:01.000,BAW123,35000,450,180.5,51.5075,-0.1278,,,,,,0\n"
	if _, err := conn.Write([]byte(lines)); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { return sink.count() == 2 && len(sink.deadLetters()) == 2 }, 2*time.Second) {
		t.Fatalf("observations=%d dlq=%v skipped=%v", sink.count(), sink.deadLetters(), sink.skipped)
	}
	o := sink.obs[0]
	if o.Decoder != "sbs" || o.Source != "ADS_B" || o.SensorID == "" || string(o.Raw) == "" || o.RawContentType != "text/plain" || o.ReceivedAt.IsZero() {
		t.Errorf("observation = %+v", o)
	}
	if sink.skips("msg_8_empty") != 1 {
		t.Errorf("skips = %v", sink.skipped)
	}
	h := d.Health()
	if !h.Connected || h.Detail["connections"] != 1 || h.Detail["decoder"] != "sbs" {
		t.Errorf("health = %+v", h)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("driver did not stop")
	}
}

// fakeTransport emits the frames it is given as fast as it can.
type fakeTransport struct {
	frames  [][]byte
	results []error
}

func (f *fakeTransport) Run(ctx context.Context, emit transport.Emit) error {
	for _, p := range f.frames {
		f.results = append(f.results, emit(ctx, transport.Frame{Payload: p, ReceivedAt: time.Now()}))
	}
	<-ctx.Done()
	return nil
}
func (f *fakeTransport) State() transport.State { return transport.State{Connected: true} }

type panicDecoder struct{}

func (panicDecoder) Decode(transport.Frame) ([]*obs.Observation, error) { panic("decoder bug") }
func (panicDecoder) Close() error                                       { return nil }

func newFakeDriver(policy string, tr transport.Transport, dec decoder.Decoder) *Driver {
	return &Driver{spec: driver.Spec{FeedID: "f"}, logger: zap.NewNop(), tcfg: transport.Config{Kind: "fake"}, transport: tr, decoder: dec, ddef: decoder.Definition{Name: "fake", Source: "X"}, policy: policy}
}

func TestDecoderPanicIsDeadLettered(t *testing.T) {
	tr := &fakeTransport{frames: [][]byte{[]byte("x")}}
	d := newFakeDriver(driver.BackpressureBlock, tr, panicDecoder{})
	sink := newSink()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = d.Run(ctx, sink) }()
	if !waitFor(func() bool { return len(sink.deadLetters()) == 1 }, time.Second) {
		t.Fatalf("dlq = %v", sink.deadLetters())
	}
	if dl := sink.deadLetters()[0]; dl != "panic:x" {
		t.Errorf("dead letter = %q", dl)
	}
	cancel()
}

func TestBackpressurePolicies(t *testing.T) {
	saved := FrameBuffer
	FrameBuffer = 1
	defer func() { FrameBuffer = saved }()
	many := make([][]byte, 20)
	for i := range many {
		many[i] = []byte("f")
	}
	okDecoder := decoder.Stateless(func(f transport.Frame) ([]*obs.Observation, error) {
		o := &obs.Observation{SourceEntityID: "a"}
		o.SetPosition(1, 1)
		return []*obs.Observation{o}, nil
	})

	t.Run("drop", func(t *testing.T) {
		tr := &fakeTransport{frames: many}
		d := newFakeDriver(driver.BackpressureDrop, tr, okDecoder)
		sink := newSink()
		sink.slow = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = d.Run(ctx, sink) }()
		if !waitFor(func() bool { return sink.skips("dropped_backpressure") > 0 }, time.Second) {
			t.Fatal("expected drops")
		}
		close(sink.slow)
		cancel()
		<-done
		if sink.skips("dropped_backpressure")+sink.count() < len(many)-2 {
			t.Errorf("frames unaccounted: dropped=%d emitted=%d", sink.skips("dropped_backpressure"), sink.count())
		}
	})
	t.Run("nack", func(t *testing.T) {
		tr := &fakeTransport{frames: many}
		d := newFakeDriver(driver.BackpressureNack, tr, okDecoder)
		sink := newSink()
		sink.slow = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = d.Run(ctx, sink) }()
		if !waitFor(func() bool { return sink.skips("nacked_backpressure") > 0 }, time.Second) {
			t.Fatal("expected nacks")
		}
		close(sink.slow)
		cancel()
		<-done
		nacked := 0
		for _, r := range tr.results {
			if errors.Is(r, transport.ErrBackpressure) {
				nacked++
			}
		}
		if nacked == 0 || nacked != sink.skips("nacked_backpressure") {
			t.Errorf("nacked=%d counted=%d", nacked, sink.skips("nacked_backpressure"))
		}
	})
	t.Run("block", func(t *testing.T) {
		tr := &fakeTransport{frames: many}
		d := newFakeDriver(driver.BackpressureBlock, tr, okDecoder)
		sink := newSink()
		sink.slow = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = d.Run(ctx, sink) }()
		time.Sleep(50 * time.Millisecond)
		if sink.count() != 0 || sink.skips("dropped_backpressure") != 0 {
			t.Fatalf("blocked transport must neither emit nor drop: %d %d", sink.count(), sink.skips("dropped_backpressure"))
		}
		close(sink.slow)
		if !waitFor(func() bool { return sink.count() == len(many) }, 2*time.Second) {
			t.Fatalf("after unblocking all frames must flow: %d", sink.count())
		}
		cancel()
		<-done
		if sink.blocked <= 0 {
			t.Error("blocked time should be accounted")
		}
	})
}

func TestNewRejectsBadConfigs(t *testing.T) {
	cases := map[string]string{
		"unknown transport":   `{"transport":{"kind":"carrier-pigeon"},"decoder":{"name":"sbs"}}`,
		"unknown decoder":     `{"transport":{"kind":"tcpserver","address":":0"},"decoder":{"name":"morse"}}`,
		"message on stream":   `{"transport":{"kind":"tcpserver","address":":0"},"framing":"message","decoder":{"name":"sbs"}}`,
		"missing decoder":     `{"transport":{"kind":"tcpserver","address":":0"}}`,
		"bad address":         `{"transport":{"kind":"tcpserver","address":"nope"},"decoder":{"name":"sbs"}}`,
		"gps needs entity id": `{"transport":{"kind":"tcpserver","address":":0"},"decoder":{"name":"nmea-gps"}}`,
	}
	for name, cfg := range cases {
		if _, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(cfg)}, driver.Deps{Logger: zap.NewNop()}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := New(driver.Spec{FeedID: "x", Backpressure: "maybe", Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":":0"},"decoder":{"name":"sbs"}}`)}, driver.Deps{Logger: zap.NewNop()}); err == nil {
		t.Error("unknown backpressure policy must be rejected")
	}
}

func TestRejectReasonAndDecoderContentType(t *testing.T) {
	tr := &fakeTransport{frames: [][]byte{[]byte("rejected"), []byte("kept"), []byte("defaulted")}}
	dec := decoder.Stateless(func(f transport.Frame) ([]*obs.Observation, error) {
		switch string(f.Payload) {
		case "rejected":
			return nil, decoder.Reject("missing_identity", errors.New("no identity"))
		case "kept":
			o := &obs.Observation{SourceEntityID: "a", RawContentType: "application/x-protobuf"}
			o.SetPosition(1, 1)
			return []*obs.Observation{o}, nil
		}
		o := &obs.Observation{SourceEntityID: "b"}
		o.SetPosition(1, 1)
		return []*obs.Observation{o}, nil
	})
	d := newFakeDriver(driver.BackpressureBlock, tr, dec)
	d.ddef.ContentType = "text/plain"
	sink := newSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx, sink) }()
	if !waitFor(func() bool { return sink.count() == 2 && len(sink.deadLetters()) == 1 }, time.Second) {
		t.Fatalf("observations=%d dlq=%v", sink.count(), sink.deadLetters())
	}
	if dl := sink.deadLetters()[0]; dl != "missing_identity:rejected" {
		t.Errorf("dead letter = %q, want it filed under the decoder's reason", dl)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if got := sink.obs[0].RawContentType; got != "application/x-protobuf" {
		t.Errorf("a decoder-set content type = %q, want it kept", got)
	}
	if got := sink.obs[1].RawContentType; got != "text/plain" {
		t.Errorf("content type without a decoder value = %q, want the definition's", got)
	}
}

func TestApplySecretsAndMalformedCount(t *testing.T) {
	var tcfg transport.Config
	ApplySecrets(&tcfg, map[string]string{"password": "p", "tlsCert": "C", "tlsKey": "K", "clientCa": "CC", "serverCa": "SC"})
	if tcfg.Password != "p" || string(tcfg.CertPEM) != "C" || string(tcfg.KeyPEM) != "K" || string(tcfg.ClientCAPEM) != "CC" || string(tcfg.CAPEM) != "SC" {
		t.Errorf("tcfg = %+v", tcfg)
	}
	d, err := NewFromParts(driver.Spec{FeedID: "f"}, driver.Deps{Logger: zap.NewNop()},
		transport.Config{Kind: "udp", Address: "127.0.0.1:0"}, "", decoder.Config{Name: "sentinel"})
	if err != nil {
		t.Fatal(err)
	}
	sink := newSink()
	d.decodeOne(context.Background(), transport.Frame{Payload: []byte{0xff, 0xff, 0xff}}, sink)
	if d.Health().Detail["malformed"] != int64(1) || len(sink.deadLetters()) != 1 {
		t.Errorf("detail = %+v, dlq = %v", d.Health().Detail, sink.deadLetters())
	}
}
