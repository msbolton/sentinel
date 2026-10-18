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
	mu  sync.Mutex
	obs []*obs.Observation
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

// start runs a driver bound to a free loopback port until the test ends and
// returns it with a connected sender once the socket is bound. The sender is
// connected only after the bind, so a datagram cannot go to a closed port.
func start(t *testing.T, cfg map[string]any) (*Driver, *memSink, net.Conn) {
	t.Helper()
	port := freePort(t)
	cfg["port"], cfg["address"] = port, "127.0.0.1"
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
