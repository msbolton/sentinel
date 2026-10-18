package stream

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"

	_ "github.com/sentinel/ingest-service/internal/source/decoder/sbs"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/sentinelobs"
	_ "github.com/sentinel/ingest-service/internal/source/transport/tcpserver"
	_ "github.com/sentinel/ingest-service/internal/source/transport/udp"
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

// provSink is a memSink that also takes the provenance of a dead letter, as
// the feed runner does: the stream driver prefers that to the plain
// DeadLetter, so a dead letter lands in letters and not in the memSink's.
type provSink struct {
	*memSink
	pmu     sync.Mutex
	letters []provLetter
}

type provLetter struct {
	raw, reason string
	cause       error
	p           driver.Provenance
}

func (s *provSink) DeadLetterFrom(raw []byte, reason string, cause error, p driver.Provenance) {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	s.letters = append(s.letters, provLetter{string(raw), reason, cause, p})
}

func (s *provSink) fromLetters() []provLetter {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	return append([]provLetter(nil), s.letters...)
}

// sendGarbage runs a tcpserver stream feed with the sentinel decoder into
// sink, sends it one line that is not a sentinel.v1.Observation, and returns
// the driver with the client's end of the connection, whose local address is
// the sender as the server sees it.
func sendGarbage(t *testing.T, sink driver.Sink) (*Driver, net.Conn) {
	t.Helper()
	addr := freePort(t)
	spec := driver.Spec{FeedID: "g", Driver: ID, Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":"` + addr + `"},"framing":"line","decoder":{"name":"sentinel"}}`)}
	d, err := New(spec, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, sink) }()
	t.Cleanup(func() { cancel(); <-done })
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
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Write([]byte("garbage\n")); err != nil {
		t.Fatal(err)
	}
	return d.(*Driver), conn
}

// senderStats is the senders list of a listening transport in the driver's
// health detail.
func senderStats(t *testing.T, d *Driver) []transport.SenderStat {
	t.Helper()
	list, ok := d.Health().Detail["senders"].([]transport.SenderStat)
	if !ok {
		t.Fatalf("detail.senders = %T, want []transport.SenderStat", d.Health().Detail["senders"])
	}
	return list
}

// An undecodable message is dead-lettered with the decoder that refused it,
// the sender it came from and the time it arrived, and the listener counts it
// against that sender, so an integrator can see which sender is at fault.
func TestDeadLetterCarriesProvenanceAndNotesSender(t *testing.T) {
	sink := &provSink{memSink: newSink()}
	before := time.Now().Add(-time.Second)
	d, conn := sendGarbage(t, sink)
	if !waitFor(func() bool { return len(sink.fromLetters()) == 1 }, 2*time.Second) {
		t.Fatalf("no dead letter with provenance; plain dead letters: %v", sink.deadLetters())
	}
	got := sink.fromLetters()[0]
	if got.reason != "parse_error" || got.raw != "garbage" || got.cause == nil {
		t.Errorf("dead letter = %+v, want garbage under parse_error with its cause", got)
	}
	if got.p.Decoder != "sentinel" || got.p.Sender != conn.LocalAddr().String() {
		t.Errorf("provenance = %+v, want decoder sentinel and sender %s", got.p, conn.LocalAddr())
	}
	if got.p.ReceivedAt.Before(before) || got.p.ReceivedAt.After(time.Now()) {
		t.Errorf("receivedAt = %v, want the time the frame arrived (after %v)", got.p.ReceivedAt, before)
	}
	if plain := sink.deadLetters(); len(plain) != 0 {
		t.Errorf("a sink that takes provenance also got the plain dead letter %v", plain)
	}

	senders := senderStats(t, d)
	if len(senders) != 1 {
		t.Fatalf("senders = %+v, want the one client", senders)
	}
	s := senders[0]
	if s.Remote != got.p.Sender || s.Malformed != 1 || s.LastReason != "parse_error" || s.LastError != got.cause.Error() || s.LastMalformedAt == nil {
		t.Errorf("sender = %+v, want one undecodable message, reason parse_error, error %q", s, got.cause)
	}
	if s.Messages != 1 || !s.Connected {
		t.Errorf("sender = %+v, want its one message counted and the connection still open", s)
	}
}

// A sink that does not take provenance (every polled driver's tests, and any
// sink written before it existed) still gets the dead letter, and the sender
// is still noted.
func TestPlainSinkStillGetsDeadLetters(t *testing.T) {
	sink := newSink()
	d, conn := sendGarbage(t, sink)
	if !waitFor(func() bool { return len(sink.deadLetters()) == 1 }, 2*time.Second) {
		t.Fatalf("dead letters = %v", sink.deadLetters())
	}
	if got := sink.deadLetters()[0]; got != "parse_error:garbage" {
		t.Errorf("dead letter = %q", got)
	}
	senders := senderStats(t, d)
	if len(senders) != 1 || senders[0].Remote != conn.LocalAddr().String() || senders[0].Malformed != 1 || senders[0].LastReason != "parse_error" {
		t.Errorf("senders = %+v, want the client with one undecodable message", senders)
	}
}

// A transport that cannot tell senders apart (it is not a listener, so there is
// no sender to note) still has the provenance name the decoder and the
// frame's origin, whatever that is: a topic, a queue or an address.
func TestProvenanceFromATransportWithoutSenders(t *testing.T) {
	received := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	tr := &originTransport{frame: transport.Frame{Payload: []byte("x"), ReceivedAt: received, Remote: "sensors/ais"}}
	d := newFakeDriver(driver.BackpressureBlock, tr, panicDecoder{})
	sink := &provSink{memSink: newSink()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, sink) }()
	defer func() { cancel(); <-done }()
	if !waitFor(func() bool { return len(sink.fromLetters()) == 1 }, time.Second) {
		t.Fatal("no dead letter")
	}
	got := sink.fromLetters()[0]
	if got.reason != "panic" || got.p.Decoder != "fake" || got.p.Sender != "sensors/ais" || !got.p.ReceivedAt.Equal(received) {
		t.Errorf("dead letter = %+v, want the panic from the fake decoder, frame origin sensors/ais, received %v", got, received)
	}
}

// A WebSocket feed's remote is the URL it is configured with, which usually
// carries an API key in its query. The sender on the dead letter and the
// remote a listener is told to note have the user, query and fragment removed,
// so none of them reaches the dead-letter topic or the health detail.
func TestURLCredentialsStayOutOfDeadLetterProvenance(t *testing.T) {
	const remote = "wss://user:tok@feed.example.com:443/stream?key=abc#top"
	const want = "wss://feed.example.com:443/stream"
	tr := &noteTransport{originTransport: originTransport{frame: transport.Frame{Payload: []byte("x"), ReceivedAt: time.Now().UTC(), Remote: remote}}}
	d := newFakeDriver(driver.BackpressureBlock, tr, panicDecoder{})
	d.noter = tr
	sink := &provSink{memSink: newSink()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, sink) }()
	defer func() { cancel(); <-done }()
	if !waitFor(func() bool { return len(sink.fromLetters()) == 1 }, time.Second) {
		t.Fatal("no dead letter")
	}
	if got := sink.fromLetters()[0].p.Sender; got != want {
		t.Errorf("sender = %q, want %q", got, want)
	}
	if notes := tr.notes(); len(notes) != 1 || notes[0] != want {
		t.Errorf("noted remotes = %q, want only %q", notes, want)
	}
}

// An observation whose message names no sensor is attributed to the remote its
// frame came from, which for a WebSocket feed is the URL it is configured
// with, key and all. The default drops the credentials, as the dead-letter
// sender does; a sensor id the message supplies is the sender's own and is
// kept as sent.
func TestSensorIDDefaultsToTheRedactedRemote(t *testing.T) {
	for name, tc := range map[string]struct{ remote, sensorID, want string }{
		"a URL remote loses its key":            {"wss://feed.example.com:443/stream?key=abc", "", "wss://feed.example.com:443/stream"},
		"a URL remote loses its user as well":   {"wss://user:tok@feed.example.com/stream?key=abc#top", "", "wss://feed.example.com/stream"},
		"a peer address is kept":                {"10.0.0.5:4000", "", "10.0.0.5:4000"},
		"a topic is kept":                       {"sensors/ais/1", "", "sensors/ais/1"},
		"a sensor id the message names is kept": {"wss://feed.example.com/stream?key=abc", "wss://rx.example.com/s?site=7", "wss://rx.example.com/s?site=7"},
	} {
		t.Run(name, func(t *testing.T) {
			tr := &originTransport{frame: transport.Frame{Payload: []byte("x"), ReceivedAt: time.Now().UTC(), Remote: tc.remote}}
			dec := decoder.Stateless(func(transport.Frame) ([]*obs.Observation, error) {
				o := &obs.Observation{SourceEntityID: "a", SensorID: tc.sensorID}
				o.SetPosition(1, 1)
				return []*obs.Observation{o}, nil
			})
			d := newFakeDriver(driver.BackpressureBlock, tr, dec)
			sink := newSink()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); _ = d.Run(ctx, sink) }()
			defer func() { cancel(); <-done }()
			if !waitFor(func() bool { return sink.count() == 1 }, time.Second) {
				t.Fatal("no observation")
			}
			sink.mu.Lock()
			defer sink.mu.Unlock()
			if got := sink.obs[0].SensorID; got != tc.want {
				t.Errorf("sensor id = %q, want %q", got, tc.want)
			}
		})
	}
}

// originTransport emits one frame as given.
type originTransport struct{ frame transport.Frame }

func (o *originTransport) Run(ctx context.Context, emit transport.Emit) error {
	_ = emit(ctx, o.frame)
	<-ctx.Done()
	return nil
}
func (o *originTransport) State() transport.State { return transport.State{Connected: true} }

// noteTransport is an originTransport that, as a listener does, takes note of
// each undecodable message: it keeps the remote it was told of.
type noteTransport struct {
	originTransport
	mu    sync.Mutex
	noted []string
}

func (n *noteTransport) NoteMalformed(remote, _, _ string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.noted = append(n.noted, remote)
}

func (n *noteTransport) notes() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.noted...)
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
		d, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(cfg)}, driver.Deps{Logger: zap.NewNop()})
		if err == nil {
			t.Errorf("%s: expected an error", name)
		}
		// An untyped nil: a typed nil *Driver in the interface is not == nil.
		if d != nil {
			t.Errorf("%s: driver = %#v alongside the error, want nil", name, d)
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

// A tcpserver feed that sets no maxConnections caps its connections at
// DefaultMaxConnections, as the tcp driver does; a value it does set, 0
// (unlimited) included, is kept. New decides this from the raw config,
// because the parsed config cannot tell an absent key from 0. Only a
// tcpserver has connections to cap.
func TestTCPServerDefaultsMaxConnections(t *testing.T) {
	cases := []struct {
		name, transport string
		want            int
	}{
		{"tcpserver, absent", `{"kind":"tcpserver","address":"127.0.0.1:0"}`, 50},
		{"tcpserver, null", `{"kind":"tcpserver","address":"127.0.0.1:0","maxConnections":null}`, 50},
		{"tcpserver, explicit 0 is unlimited", `{"kind":"tcpserver","address":"127.0.0.1:0","maxConnections":0}`, 0},
		{"tcpserver, explicit 5", `{"kind":"tcpserver","address":"127.0.0.1:0","maxConnections":5}`, 5},
		{"udp, absent", `{"kind":"udp","address":"127.0.0.1:0"}`, 0},
	}
	for _, tc := range cases {
		cfg := `{"transport":` + tc.transport + `,"framing":"line","decoder":{"name":"sbs"}}`
		d, err := New(driver.Spec{FeedID: "f", Config: json.RawMessage(cfg)}, driver.Deps{Logger: zap.NewNop()})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := d.(*Driver).tcfg.MaxConnections; got != tc.want {
			t.Errorf("%s: maxConnections = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A client CA verifies only the certificates a listener asks for, so a stream
// listener that holds the clientCa secret without requireClientCert is
// refused, as the tcp driver refuses it, instead of accepting every sender
// unchecked.
func TestStreamRefusesClientCAWithoutRequireClientCert(t *testing.T) {
	ca := transporttest.NewCA(t)
	pair := ca.Issue(t, "localhost")
	secrets := map[string]string{"tlsCert": string(pair.CertPEM), "tlsKey": string(pair.KeyPEM), "clientCa": string(ca.CertPEM)}
	build := func(tlsSection string) error {
		cfg := `{"transport":{"kind":"tcpserver","address":"127.0.0.1:0"` + tlsSection + `},"framing":"line","decoder":{"name":"sbs"}}`
		_, err := New(driver.Spec{FeedID: "f", Config: json.RawMessage(cfg), Secrets: secrets}, driver.Deps{Logger: zap.NewNop()})
		return err
	}
	for name, tlsSection := range map[string]string{
		"no tls section":        ``,
		"TLS off":               `,"tls":{"enabled":false,"requireClientCert":true}`,
		"requireClientCert off": `,"tls":{"enabled":true}`,
	} {
		err := build(tlsSection)
		if err == nil || !strings.Contains(err.Error(), "clientCa") || !strings.Contains(err.Error(), "requireClientCert") {
			t.Errorf("%s: err = %v, want one naming clientCa and requireClientCert", name, err)
		}
	}
	if err := build(`,"tls":{"enabled":true,"requireClientCert":true}`); err != nil {
		t.Errorf("mutual TLS with a client CA: %v", err)
	}
}

// The schema's maxConnections default is what the form shows a tcpserver feed
// will use when it sets none, and New applies DefaultMaxConnections: they must
// stay one number.
func TestSchemaDefaultMatchesDefaultMaxConnections(t *testing.T) {
	var doc struct {
		Properties struct {
			Transport struct {
				Properties struct {
					MaxConnections struct {
						Default *int `json:"default"`
					} `json:"maxConnections"`
				} `json:"properties"`
			} `json:"transport"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatal(err)
	}
	def := doc.Properties.Transport.Properties.MaxConnections.Default
	if def == nil {
		t.Fatalf("the schema gives transport.maxConnections no default, want DefaultMaxConnections (%d)", DefaultMaxConnections)
	}
	if *def != DefaultMaxConnections {
		t.Errorf("schema transport.maxConnections default = %d, want DefaultMaxConnections (%d)", *def, DefaultMaxConnections)
	}
}

// The form titles each TLS secret from the schema's x-secrets and puts no
// condition on any: a stream's TLS is not always governed by tls.enabled (a
// wss:// URL or an ssl:// broker turns it on too), so each secret is always
// offered.
func TestSchemaTitlesTheTLSSecretsWithoutConditions(t *testing.T) {
	var doc struct {
		Secrets map[string]map[string]json.RawMessage `json:"x-secrets"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatal(err)
	}
	def, ok := driver.Lookup(ID)
	if !ok {
		t.Fatal("the stream driver is not registered")
	}
	for _, name := range []string{"tlsCert", "tlsKey", "clientCa", "serverCa"} {
		if _, ok := doc.Secrets[name]; !ok {
			t.Errorf("x-secrets has no entry for %s", name)
		}
	}
	for name, meta := range doc.Secrets {
		if !slices.Contains(def.SecretFields, name) {
			t.Errorf("x-secrets describes %s, which is not one of the driver's secret fields %v", name, def.SecretFields)
		}
		var title string
		if err := json.Unmarshal(meta["title"], &title); err != nil || title == "" {
			t.Errorf("x-secrets.%s has no title", name)
		}
		for _, condition := range []string{"showWhen", "requiredWhen"} {
			if _, has := meta[condition]; has {
				t.Errorf("x-secrets.%s has %s, but a stream's TLS secrets are always offered", name, condition)
			}
		}
	}
}
