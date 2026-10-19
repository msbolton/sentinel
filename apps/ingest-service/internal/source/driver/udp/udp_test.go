package udp

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	// The pipeline pieces the driver assembles. The all package would pull
	// in this package and so cannot be imported from its own test.
	_ "github.com/sentinel/ingest-service/internal/source/decoder/cot"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/jsonmapped"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/link16"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/nmeaais"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/nmeagps"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/sbs"
	_ "github.com/sentinel/ingest-service/internal/source/decoder/sentinelobs"
	_ "github.com/sentinel/ingest-service/internal/source/transport/udp"
)

const waitTimeout = 5 * time.Second

type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	letters []deadLetter
}

// deadLetter is a dead letter with the provenance the feed runner would get.
type deadLetter struct {
	raw, reason string
	p           driver.Provenance
}

func (s *memSink) Emit(_ context.Context, o *obs.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, o)
	return nil
}
func (s *memSink) DeadLetter([]byte, string, error) {}
func (s *memSink) Skipped(string)                   {}
func (s *memSink) Succeeded(int)                    {}
func (s *memSink) Blocked(time.Duration)            {}
func (s *memSink) count() int                       { s.mu.Lock(); defer s.mu.Unlock(); return len(s.obs) }
func (s *memSink) ids() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, o := range s.obs {
		out = append(out, o.SourceEntityID)
	}
	return out
}

// DeadLetterFrom is what the feed runner offers a driver beside DeadLetter;
// the stream driver prefers it.
func (s *memSink) DeadLetterFrom(raw []byte, reason string, _ error, p driver.Provenance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.letters = append(s.letters, deadLetter{string(raw), reason, p})
}
func (s *memSink) deadLetters() []deadLetter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]deadLetter(nil), s.letters...)
}

// freePort is a port free for UDP (one free for TCP may still be taken).
func freePort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func newDriver(t *testing.T, cfg map[string]any) (*Driver, error) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(driver.Spec{FeedID: "u", Driver: ID, Config: raw}, driver.Deps{Logger: zap.NewNop(), ListenerHost: "sentinel.example"})
	if err != nil {
		return nil, err
	}
	return d.(*Driver), nil
}

// run starts a driver until the test ends and waits for it to bind its socket.
func run(t *testing.T, cfg map[string]any) (*Driver, *memSink) {
	t.Helper()
	d, err := newDriver(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sink := &memSink{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, sink) }()
	t.Cleanup(func() { cancel(); <-done })
	if !transporttest.WaitFor(func() bool { return d.Health().Connected }, waitTimeout) {
		t.Fatal("nothing listening")
	}
	return d, sink
}

// start runs a driver bound to a free loopback port until the test ends and
// returns it with a connected sender once the socket is bound. The sender is
// connected only after the bind, so a datagram cannot go to a closed port.
func start(t *testing.T, cfg map[string]any) (*Driver, *memSink, net.Conn) {
	t.Helper()
	port := freePort(t)
	cfg["port"], cfg["address"] = port, "127.0.0.1"
	d, sink := run(t, cfg)
	c, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return d, sink, c
}

// counter is a listen counter in the health detail.
func counter(d *Driver, key string) int64 {
	n, _ := d.Health().Detail[key].(int64)
	return n
}

func protobufDatagram(id string) []byte {
	b, _ := proto.Marshal(&sentinelv1.Observation{SourceEntityId: id, Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}})
	return b
}

func TestProtobufDatagramIsOneMessage(t *testing.T) {
	cfg := map[string]any{"encoding": "protobuf"}
	d, sink, c := start(t, cfg)
	// A binary message may hold a newline byte; it must not be split.
	b := protobufDatagram("p1\nx")
	if _, err := c.Write(b); err != nil {
		t.Fatal(err)
	}
	if !transporttest.WaitFor(func() bool { return sink.count() >= 1 }, waitTimeout) {
		t.Fatal("no observation from a protobuf datagram")
	}
	if ids := sink.ids(); len(ids) != 1 || ids[0] != "p1\nx" {
		t.Errorf("observations = %q, want the one whole message", ids)
	}

	h := d.Health().Detail
	l, _ := h["listen"].(map[string]any)
	if l["protocol"] != "udp" || l["host"] != "sentinel.example" || l["port"] != cfg["port"] || h["format"] != "sentinel" || h["tls"] != false || h["allowlist"] != "open" {
		t.Errorf("detail = %+v", h)
	}
	if _, ok := h["connect"]; ok {
		t.Errorf("a listener reports no connect target: %+v", h)
	}
}

// A datagram nothing can decode is dead-lettered with the exact socket it came
// from (ip:port), and the sender's row in the health detail counts it. That row
// is named by the sender's IP, which both sockets here share, so it holds the
// messages of both and the one that did not decode.
func TestMalformedDatagramNamesItsSender(t *testing.T) {
	d, sink, bad := start(t, map[string]any{"encoding": "protobuf"})
	good, err := net.Dial("udp", bad.RemoteAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = good.Close() })

	before := time.Now().Add(-time.Second)
	if _, err := good.Write(protobufDatagram("ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Write([]byte("garbage")); err != nil {
		t.Fatal(err)
	}
	if !transporttest.WaitFor(func() bool { return len(sink.deadLetters()) == 1 && sink.count() == 1 }, waitTimeout) {
		t.Fatalf("dead letters = %+v, observations = %q, want one of each", sink.deadLetters(), sink.ids())
	}

	dl := sink.deadLetters()[0]
	if dl.reason != "parse_error" || dl.raw != "garbage" {
		t.Errorf("dead letter = %+v, want garbage under parse_error", dl)
	}
	if dl.p.Decoder != "sentinel" || dl.p.Sender != bad.LocalAddr().String() {
		t.Errorf("provenance = %+v, want decoder sentinel and sender %s, the exact socket", dl.p, bad.LocalAddr())
	}
	if dl.p.ReceivedAt.Before(before) || dl.p.ReceivedAt.After(time.Now()) {
		t.Errorf("receivedAt = %v, want the time the datagram arrived (after %v)", dl.p.ReceivedAt, before)
	}

	senders, ok := d.Health().Detail["senders"].([]transport.SenderStat)
	if !ok {
		t.Fatalf("detail.senders = %T", d.Health().Detail["senders"])
	}
	if len(senders) != 1 || senders[0].Remote != "127.0.0.1" {
		t.Fatalf("senders = %+v, want the one row of the IP both sockets share, and none for the socket's port", senders)
	}
	if s := senders[0]; s.Messages != 2 || s.Malformed != 1 || s.LastReason != "parse_error" || s.LastError == "" || s.LastMalformedAt == nil {
		t.Errorf("the sender = %+v, want 2 messages, 1 of them undecodable, with its reason and error", s)
	}
}

// Two senders on different addresses (an IPv4 one reaching a dual-stack socket
// as an IPv4-mapped IPv6 address, and an IPv6 one): the undecodable datagram is
// blamed on its own sender's row and on no other.
func TestMalformedDatagramBlamesOnlyItsSender(t *testing.T) {
	probe, err := net.ListenPacket("udp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()
	// With no address the driver binds the wildcard: one socket for both families.
	d, sink := run(t, map[string]any{"encoding": "protobuf", "port": port})
	bad, err := net.Dial("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bad.Close() })
	good, err := net.Dial("udp6", net.JoinHostPort("::1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = good.Close() })

	if _, err := good.Write(protobufDatagram("ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Write([]byte("garbage")); err != nil {
		t.Fatal(err)
	}
	if !transporttest.WaitFor(func() bool { return len(sink.deadLetters()) == 1 && sink.count() == 1 }, waitTimeout) {
		t.Fatalf("dead letters = %+v, observations = %q, want one of each", sink.deadLetters(), sink.ids())
	}
	if got := sink.deadLetters()[0].p.Sender; got != bad.LocalAddr().String() {
		t.Errorf("dead letter sender = %q, want the exact socket %s", got, bad.LocalAddr())
	}

	senders, _ := d.Health().Detail["senders"].([]transport.SenderStat)
	byRemote := map[string]transport.SenderStat{}
	for _, s := range senders {
		byRemote[s.Remote] = s
	}
	if len(byRemote) != 2 {
		t.Fatalf("senders = %+v, want 127.0.0.1 and ::1", senders)
	}
	if s := byRemote["127.0.0.1"]; s.Messages != 1 || s.Malformed != 1 || s.LastReason != "parse_error" || s.LastError == "" || s.LastMalformedAt == nil {
		t.Errorf("the sender of the garbage = %+v, want one message, undecodable, with its reason and error", s)
	}
	if s := byRemote["::1"]; s.Messages != 1 || s.Malformed != 0 || s.LastReason != "" || s.LastError != "" || s.LastMalformedAt != nil {
		t.Errorf("the sender of the good datagram = %+v, want one message and nothing malformed", s)
	}
}

func TestJSONLinesInOneDatagram(t *testing.T) {
	_, sink, c := start(t, map[string]any{"encoding": "json"})
	payload := `{"sourceEntityId":"u1","position":{"latitude":1,"longitude":2}}` + "\r\n" + `{"sourceEntityId":"u2","position":{"latitude":3,"longitude":4}}`
	if _, err := c.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if !transporttest.WaitFor(func() bool { return sink.count() >= 2 }, waitTimeout) {
		t.Fatalf("observations = %q, want u1 and u2", sink.ids())
	}
	if ids := sink.ids(); len(ids) != 2 || ids[0] != "u1" || ids[1] != "u2" {
		t.Errorf("observations = %q, want u1 and u2", ids)
	}
}

// The listener's own checks apply through the driver: senders outside the
// allowlist and datagrams over maxMessageBytes are dropped and counted.
func TestAllowlistAndSizeLimitApply(t *testing.T) {
	d, sink, c := start(t, map[string]any{"encoding": "json", "allow": []string{"192.0.2.1"}})
	_, _ = c.Write([]byte(`{"sourceEntityId":"denied","position":{"latitude":1,"longitude":2}}`))
	if !transporttest.WaitFor(func() bool { return counter(d, "rejected") == 1 }, waitTimeout) {
		t.Fatalf("rejected = %d, want 1", counter(d, "rejected"))
	}
	if sink.count() != 0 {
		t.Errorf("a sender outside the allowlist produced %q", sink.ids())
	}
	if got := d.Health().Detail["allowlist"]; got != 1 {
		t.Errorf("allowlist = %v, want 1", got)
	}

	d, sink, c = start(t, map[string]any{"encoding": "json", "maxMessageBytes": 64})
	_, _ = c.Write([]byte(`{"sourceEntityId":"toolong","position":{"latitude":1,"longitude":2}}` + strings.Repeat(" ", 64)))
	if !transporttest.WaitFor(func() bool { return counter(d, "oversize") == 1 }, waitTimeout) {
		t.Fatalf("oversize = %d, want 1", counter(d, "oversize"))
	}
	_, _ = c.Write([]byte(`{"sourceEntityId":"ok"}`))
	if !transporttest.WaitFor(func() bool { return sink.count() == 1 }, waitTimeout) || sink.ids()[0] != "ok" {
		t.Errorf("observations = %q, want only ok", sink.ids())
	}
}

// maxMessagesPerSec is the configured rate; 0 means unlimited and unset means
// 1000 a second. The limit counts messages, not datagrams, so a datagram of
// lines can be partly dropped. (A burst can also overrun the stream queue,
// which drops under udp's default backpressure, so observations are not
// compared with the accepted count.)
func TestMessageRate(t *testing.T) {
	line := `{"sourceEntityId":"r"}` + "\n"
	for name, tc := range map[string]struct {
		cfg       map[string]any
		lines     int // per datagram
		datagrams int
		ok        func(accepted, limited int64) bool
	}{
		// Datagrams stay small: loopback refuses large ones on some systems.
		"default 1000": {map[string]any{"encoding": "json"}, 300, 7, func(a, l int64) bool { return a >= 1000 && l > 0 }},
		"unlimited":    {map[string]any{"encoding": "json", "maxMessagesPerSec": 0}, 300, 7, func(a, l int64) bool { return a == 2100 && l == 0 }},
		"set to 2":     {map[string]any{"encoding": "json", "maxMessagesPerSec": 2}, 5, 1, func(a, l int64) bool { return a == 2 && l == 3 }},
	} {
		d, _, c := start(t, tc.cfg)
		for i := 0; i < tc.datagrams; i++ {
			if _, err := c.Write([]byte(strings.Repeat(line, tc.lines))); err != nil {
				t.Fatal(err)
			}
		}
		total := int64(tc.lines * tc.datagrams)
		if !transporttest.WaitFor(func() bool { return counter(d, "accepted")+counter(d, "rateLimited") == total }, waitTimeout) {
			t.Fatalf("%s: accepted %d + rateLimited %d, want %d lines", name, counter(d, "accepted"), counter(d, "rateLimited"), total)
		}
		a, l := counter(d, "accepted"), counter(d, "rateLimited")
		if !tc.ok(a, l) {
			t.Errorf("%s: accepted %d, rateLimited %d", name, a, l)
		}
	}
}

// maxMessagesPerSecPerSender reaches the listener: one sender's lines beyond
// its own rate are dropped and counted apart from the shared limit, and the
// setting off (absent, the default) limits no sender. The lines are one
// datagram, which the listener asks of the sender's bucket at a single
// instant, so the counts are exact however slow the machine.
func TestPerSenderMessageRate(t *testing.T) {
	line := `{"sourceEntityId":"r"}` + "\n"
	for name, tc := range map[string]struct {
		cfg       map[string]any
		accepted  int64
		perSender int64
	}{
		"off by default": {map[string]any{"encoding": "json"}, 5, 0},
		"set to 2":       {map[string]any{"encoding": "json", "maxMessagesPerSecPerSender": 2}, 2, 3},
	} {
		d, _, c := start(t, tc.cfg)
		if _, err := c.Write([]byte(strings.Repeat(line, 5))); err != nil {
			t.Fatal(err)
		}
		if !transporttest.WaitFor(func() bool { return counter(d, "accepted")+counter(d, "senderRateLimited") == 5 }, waitTimeout) {
			t.Fatalf("%s: accepted %d + senderRateLimited %d, want 5 lines", name, counter(d, "accepted"), counter(d, "senderRateLimited"))
		}
		if a, l := counter(d, "accepted"), counter(d, "senderRateLimited"); a != tc.accepted || l != tc.perSender || counter(d, "rateLimited") != 0 {
			t.Errorf("%s: accepted %d, senderRateLimited %d, rateLimited %d; want %d, %d, 0", name, a, l, counter(d, "rateLimited"), tc.accepted, tc.perSender)
		}
	}
}

// receiveBufferBytes reaches the socket: a connection that asks for less than
// the default gets a smaller buffer, and its health says how big the socket's
// is. The sizes are the OS's own (Linux keeps twice what it is asked for, up
// to net.core.rmem_max), so they are compared with each other, not with a
// number.
func TestReceiveBufferBytes(t *testing.T) {
	sizeOf := func(cfg map[string]any) int64 {
		d, _, _ := start(t, cfg)
		n, _ := d.Health().Detail["receiveBufferEffectiveBytes"].(int64)
		return n
	}
	byDefault := sizeOf(map[string]any{})
	small := sizeOf(map[string]any{"receiveBufferBytes": 64 << 10})
	if byDefault == 0 && small == 0 {
		t.Skip("this platform cannot say how big a socket's receive buffer is")
	}
	if small <= 0 || byDefault <= small {
		t.Errorf("receive buffer: %d with receiveBufferBytes 65536, %d by default; want the first positive and the smaller", small, byDefault)
	}
}

func TestListenHook(t *testing.T) {
	for cfg, want := range map[string]struct {
		port int
		ok   bool
	}{
		`{"port":10110}`: {10110, true},
		// A udp connection always listens, so the port rules refuse a
		// missing or mistyped port instead of skipping the feed.
		`{}`:                            {0, true},
		`{"port":0}`:                    {0, true},
		`{"port":"x"}`:                  {0, true},
		`{"port":"10110"}`:              {0, true},
		`{"port":null}`:                 {0, true},
		`{"port":10110.5}`:              {0, true},
		`{"port":10110,"allow":"oops"}`: {10110, true},
		`{"port":10110,"format":5}`:     {10110, true},
		`{nope`:                         {0, false},
		`[]`:                            {0, false},
		``:                              {0, false},
	} {
		n, p, ok := driver.ListenOf(ID, json.RawMessage(cfg))
		if ok != want.ok || p != want.port || (ok && n != "udp") || (!ok && n != "") {
			t.Errorf("%s: got %q %d %v", cfg, n, p, ok)
		}
	}
}

// The udp transport splits a datagram into lines for "line" and keeps it
// whole for "message": binary protobuf, CoT XML and Link 16 are one message
// per datagram; JSON, NMEA and SBS may carry several lines.
func TestFramingPerFormat(t *testing.T) {
	gps := json.RawMessage(`{"sourceEntityId":"rx1"}`)
	for name, tc := range map[string]struct {
		cfg  map[string]any
		want string
	}{
		"default format":        {map[string]any{}, "message"},
		"sentinel":              {map[string]any{"format": "sentinel"}, "message"},
		"sentinel protobuf":     {map[string]any{"format": "sentinel", "encoding": "protobuf"}, "message"},
		"default format json":   {map[string]any{"encoding": "json"}, "line"},
		"sentinel json":         {map[string]any{"format": "sentinel", "encoding": "json"}, "line"},
		"cot":                   {map[string]any{"format": "cot"}, "message"},
		"cot, json encoding":    {map[string]any{"format": "cot", "encoding": "json"}, "message"},
		"link16":                {map[string]any{"format": "link16"}, "message"},
		"nmea-ais":              {map[string]any{"format": "nmea-ais"}, "line"},
		"nmea-gps":              {map[string]any{"format": "nmea-gps", "decoderOptions": gps}, "line"},
		"sbs":                   {map[string]any{"format": "sbs"}, "line"},
		"json":                  {map[string]any{"format": "json"}, "line"},
		"json, protobuf option": {map[string]any{"format": "json", "encoding": "protobuf"}, "line"},
	} {
		tc.cfg["port"] = 10110
		d, err := newDriver(t, tc.cfg)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := d.Health().Detail["framing"]; got != tc.want {
			t.Errorf("%s: framing = %v, want %s", name, got, tc.want)
		}
	}
}

func TestNewRefusals(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		"no port":             {},
		"port 0":              {"port": 0},
		"below 1024":          {"port": 80},
		"above 65535":         {"port": 70000},
		"unknown format":      {"port": 10110, "format": "xml"},
		"bad encoding":        {"port": 10110, "encoding": "yaml"},
		"bad allowlist entry": {"port": 10110, "allow": []string{"not-an-ip"}},
		"nmea-gps no id":      {"port": 10110, "format": "nmea-gps"},
		"negative buffer":     {"port": 10110, "receiveBufferBytes": -1},
		"buffer over 64 MiB":  {"port": 10110, "receiveBufferBytes": 64<<20 + 1},
	} {
		if d, err := newDriver(t, cfg); err == nil {
			t.Errorf("%s: accepted, driver %v", name, d)
		}
	}
	// The typed nil of a failed build never reaches the caller as a driver.
	if d, err := New(driver.Spec{FeedID: "u", Driver: ID, Config: json.RawMessage(`{"port":80}`)}, driver.Deps{Logger: zap.NewNop()}); err == nil || d != nil {
		t.Errorf("New with port 80 = %v, %v", d, err)
	}
}
