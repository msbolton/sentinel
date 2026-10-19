package registry

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

func udpStream(id, addr string, enabled bool) Feed {
	return Feed{ID: id, Name: id, Driver: "stream", Enabled: enabled, Config: json.RawMessage(`{"transport":{"kind":"udp","address":"` + addr + `"},"decoder":{"name":"nmea-ais"}}`)}
}

func TestParsePortSet(t *testing.T) {
	s, err := ParsePortSet("3000-3002, 5432")
	if err != nil || !s[3001] || !s[5432] || s[3003] {
		t.Errorf("set %v err %v", s, err)
	}
	for _, bad := range []string{"x", "5-3", "70000"} {
		if _, err := ParsePortSet(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if _, err := ParsePortSet(DefaultReservedPorts); err != nil {
		t.Fatal(err)
	}
}

func TestPortRules(t *testing.T) {
	reserved, _ := ParsePortSet(DefaultReservedPorts)
	others := []Feed{udpStream("pier-ais", ":10110", false)} // disabled still holds its port
	msg := func(ps []Problem) string {
		var b strings.Builder
		for _, p := range ps {
			b.WriteString(p.Path + " " + p.Message + ";")
		}
		return b.String()
	}
	if ps := CheckPorts(udpStream("new", ":10110", true), others, reserved); !strings.Contains(msg(ps), "used by connection pier-ais") {
		t.Errorf("duplicate udp port: %s", msg(ps))
	}
	// The same number on the other protocol is fine.
	tcp := Feed{ID: "t", Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":":10110"},"decoder":{"name":"cot"}}`)}
	if ps := CheckPorts(tcp, others, reserved); len(ps) != 0 {
		t.Errorf("tcp 10110 next to udp 10110: %s", msg(ps))
	}
	// Updating a connection without changing its port does not clash with itself.
	if ps := CheckPorts(udpStream("pier-ais", ":10110", true), others, reserved); len(ps) != 0 {
		t.Errorf("self clash: %s", msg(ps))
	}
	if ps := CheckPorts(udpStream("low", ":514", true), nil, reserved); !strings.Contains(msg(ps), "1024") {
		t.Errorf("privileged port: %s", msg(ps))
	}
	// 1024 is a policy floor, not a kernel limit, and the message must not
	// suggest the port needs root.
	if m := msg(CheckPorts(udpStream("low", ":514", true), nil, reserved)); !strings.Contains(m, "policy floor, not a kernel limit") || strings.Contains(m, "root") {
		t.Errorf("port floor message: %s", m)
	}
	if ps := CheckPorts(udpStream("res", ":5432", true), nil, reserved); !strings.Contains(msg(ps), "Sentinel") {
		t.Errorf("reserved port: %s", msg(ps))
	}
	// A listening transport without a numeric port is refused, not skipped.
	for _, addr := range []string{"localhost", ":http", ":abc", ":0"} {
		if ps := CheckPorts(udpStream("noport", addr, true), nil, reserved); !strings.Contains(msg(ps), "1024") {
			t.Errorf("address %q: %s", addr, msg(ps))
		}
	}
	// Connections that do not listen are not checked.
	client := Feed{ID: "c", Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"tcpclient","address":"h:80"},"decoder":{"name":"sbs"}}`)}
	if ps := CheckPorts(client, nil, reserved); len(ps) != 0 {
		t.Errorf("client: %s", msg(ps))
	}
}

// listenerTestDriver stands in for the tcp/udp drivers: top-level port and
// allow, and a Listen hook, under an id no real driver will take.
const listenerTestDriver = "test-listener"

func init() {
	driver.Register(driver.Definition{
		ID:  listenerTestDriver,
		New: func(driver.Spec, driver.Deps) (driver.Driver, error) { return nil, nil },
		Listen: func(cfg json.RawMessage) (string, int, bool) {
			var c struct {
				Port int `json:"port"`
			}
			return "udp", c.Port, json.Unmarshal(cfg, &c) == nil
		},
	})
}

func TestCheckAllow(t *testing.T) {
	udpAllow := func(allow string) Feed {
		return Feed{Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"udp","address":":6000","allow":` + allow + `}}`)}
	}
	if ps := CheckAllow(udpAllow(`["10.0.0.0/8","10.0.0.0/33"]`)); len(ps) != 1 || ps[0].Path != "/config/transport/allow" || !strings.Contains(ps[0].Message, "10.0.0.0/33") {
		t.Errorf("bad CIDR: %+v", ps)
	}
	if ps := CheckAllow(udpAllow(`["nope"]`)); len(ps) != 1 || ps[0].Path != "/config/transport/allow" {
		t.Errorf("stream allow: %+v", ps)
	}
	// A mistyped allowlist is refused, not ignored.
	for _, bad := range []string{`"10.0.0.0/8"`, `[1]`, `{"ip":"10.0.0.1"}`} {
		ps := CheckAllow(udpAllow(bad))
		if len(ps) != 1 || ps[0].Path != "/config/transport/allow" || !strings.Contains(ps[0].Message, "array") {
			t.Errorf("allow %s: %+v", bad, ps)
		}
	}
	for _, ok := range []string{`["192.168.1.20","2001:db8::/32"]`, `[]`, `null`} {
		if ps := CheckAllow(udpAllow(ok)); len(ps) != 0 {
			t.Errorf("allow %s: %+v", ok, ps)
		}
	}
	tcp := Feed{Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":":6000","allow":["192.168.1.20"]}}`)}
	if ps := CheckAllow(tcp); len(ps) != 0 {
		t.Errorf("valid tcpserver list: %+v", ps)
	}
	// Drivers with a top-level allow (tcp, udp) use /config/allow.
	top := Feed{Driver: listenerTestDriver, Config: json.RawMessage(`{"port":6000,"allow":["10.0.0.0/8","10.0.0.0/33"]}`)}
	if ps := CheckAllow(top); len(ps) != 1 || ps[0].Path != "/config/allow" {
		t.Errorf("top-level bad CIDR: %+v", ps)
	}
	top.Config = json.RawMessage(`{"port":6000,"allow":"10.0.0.0/8"}`)
	if ps := CheckAllow(top); len(ps) != 1 || ps[0].Path != "/config/allow" {
		t.Errorf("top-level mistyped: %+v", ps)
	}
	// A connection that does not listen is not checked.
	client := Feed{Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"tcpclient","address":"h:80","allow":["nope"]},"decoder":{"name":"sbs"}}`)}
	if ps := CheckAllow(client); len(ps) != 0 {
		t.Errorf("client: %+v", ps)
	}
}

// encoding/json matches an object key to a struct field ignoring case, so the
// drivers honour an allowlist spelled "Allow" or "ALLOW", under a "Transport"
// that is spelled any way too. The check reads the config the same way:
// reading the exact key only would let such a list reach a listener unchecked.
func TestCheckAllowCaseInsensitiveKey(t *testing.T) {
	const bad = `["10.0.0.0/8","10.0.0.0/33"]`
	cases := []struct {
		name, driver, config string
		path, msg            string // the one problem expected; no path when the list is acceptable
	}{
		{"stream Allow", "stream", `{"transport":{"kind":"udp","address":":47000","Allow":` + bad + `},"decoder":{"name":"nmea-ais"}}`, "/config/transport/allow", "10.0.0.0/33"},
		{"stream ALLOW", "stream", `{"transport":{"kind":"udp","address":":47000","ALLOW":` + bad + `},"decoder":{"name":"nmea-ais"}}`, "/config/transport/allow", "10.0.0.0/33"},
		{"stream with every key respelled", "stream", `{"Transport":{"Kind":"udp","Address":":47000","aLLoW":` + bad + `},"Decoder":{"Name":"nmea-ais"}}`, "/config/transport/allow", "10.0.0.0/33"},
		{"udp Allow", "udp", `{"port":47000,"Allow":` + bad + `}`, "/config/allow", "10.0.0.0/33"},
		{"tcp ALLOW", "tcp", `{"mode":"listen","port":47000,"ALLOW":` + bad + `}`, "/config/allow", "10.0.0.0/33"},
		{"mistyped under Allow", "udp", `{"port":47000,"Allow":"10.0.0.0/8"}`, "/config/allow", "array"},
		{"mistyped under ALLOW on a stream", "stream", `{"transport":{"kind":"udp","address":":47000","ALLOW":{"ip":"10.0.0.1"}},"decoder":{"name":"nmea-ais"}}`, "/config/transport/allow", "array"},
		{"an escaped spelling", "udp", `{"port":47000,"allow":` + bad + `}`, "/config/allow", "10.0.0.0/33"},
		{"a good list under Allow", "udp", `{"port":47000,"Allow":["192.168.1.20","2001:db8::/32"]}`, "", ""},
		{"an empty list under ALLOW", "tcp", `{"mode":"listen","port":47000,"ALLOW":[]}`, "", ""},
		{"null under Allow", "udp", `{"port":47000,"Allow":null}`, "", ""},
		// A key given in two spellings decodes to the last of them.
		{"the last spelling wins: bad, then good", "udp", `{"port":47000,"allow":` + bad + `,"Allow":["10.0.0.1"]}`, "", ""},
		{"the last spelling wins: good, then bad", "udp", `{"port":47000,"Allow":["10.0.0.1"],"allow":` + bad + `}`, "/config/allow", "10.0.0.0/33"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := Feed{ID: "t", Driver: c.driver, Config: json.RawMessage(c.config)}
			ps := CheckAllow(f)
			if c.path == "" {
				if len(ps) != 0 {
					t.Errorf("a list the drivers accept was refused: %+v", ps)
				}
			} else if len(ps) != 1 || ps[0].Path != c.path || !strings.Contains(ps[0].Message, c.msg) {
				t.Errorf("problems = %+v, want one at %s mentioning %q", ps, c.path, c.msg)
			}
			// The check and the driver agree on the list: building the driver
			// fails exactly when the check refuses it.
			_, err := driver.New(driver.Spec{FeedID: f.ID, Driver: f.Driver, Config: f.Config}, driver.Deps{})
			if (err != nil) != (len(ps) > 0) {
				t.Errorf("the check found %d problems but building the driver gave %v", len(ps), err)
			}
		})
	}
}

// rawAt finds an object member the way the json package finds a struct field,
// so the check reads what the drivers read. Here it is compared with the
// package itself on documents that differ in case, repeat a key or are not
// objects at all.
func TestRawAtFollowsTheDecoder(t *testing.T) {
	docs := []string{
		`{"allow":["a"]}`,
		`{"Allow":["a"]}`,
		`{"ALLOW":["a"]}`,
		`{"allow":["a"],"Allow":["b"]}`,
		`{"Allow":["b"],"allow":["a"]}`,
		`{"allow":["a"],"allow":["b"]}`,
		`{"ALLOW":["a"],"x":{"allow":["nested"]},"aLLow":["c"]}`,
		`{"allow":null}`,
		`{"allow":null,"Allow":["b"]}`,
		`{"allow":["a"],"Allow":null}`,
		`{"allow":["a"]}`,
		"{ \"allow\" :\n [ \"a\" , \"b\" ]\n}",
		`{"allo":["a"]}`,
		`{"allows":["a"]}`,
		`{"kind":"a"}`,
		"{\"Kind\":\"kelvin\"}", // the Kelvin sign folds to k, as the json package folds it
		`{"KIND":"a","Kind":"b"}`,
		`{}`,
		`[]`,
		`[{"allow":1}]`,
		`"allow"`,
		`null`,
		`7`,
		``,
		`{"allow":`,
		`{"allow":["a"],`,
		`{"allow":["a"]} x`,
	}
	for _, doc := range docs {
		var allow struct {
			V json.RawMessage `json:"allow"`
		}
		var kind struct {
			V json.RawMessage `json:"kind"`
		}
		_ = json.Unmarshal([]byte(doc), &allow)
		_ = json.Unmarshal([]byte(doc), &kind)
		if got := rawAt(json.RawMessage(doc), "allow"); string(got) != string(allow.V) {
			t.Errorf("rawAt(%q, allow) = %q, the decoder keeps %q", doc, got, allow.V)
		}
		if got := rawAt(json.RawMessage(doc), "kind"); string(got) != string(kind.V) {
			t.Errorf("rawAt(%q, kind) = %q, the decoder keeps %q", doc, got, kind.V)
		}
	}

	// A path decodes one level at a time, each level the way a struct field is found.
	for _, doc := range []string{
		`{"transport":{"allow":["a"]}}`,
		`{"Transport":{"ALLOW":["a"]}}`,
		`{"transport":{"allow":["a"]},"TRANSPORT":{"x":1}}`,
		`{"transport":{"x":1},"TRANSPORT":{"Allow":["b"]}}`,
		`{"transport":"allow"}`,
		`{"transport":null}`,
		`{"transport":[]}`,
		`{"allow":["top"]}`,
	} {
		var outer struct {
			V json.RawMessage `json:"transport"`
		}
		var inner struct {
			V json.RawMessage `json:"allow"`
		}
		_ = json.Unmarshal([]byte(doc), &outer)
		_ = json.Unmarshal(outer.V, &inner)
		if got := rawAt(json.RawMessage(doc), "transport", "allow"); string(got) != string(inner.V) {
			t.Errorf("rawAt(%q, transport, allow) = %q, the decoder keeps %q", doc, got, inner.V)
		}
	}
}

func TestPortSetRanges(t *testing.T) {
	for spec, want := range map[string][]PortRange{
		"":                    {},
		"5432":                {{5432, 5432}},
		"3000-3005,4000,4010": {{3000, 3005}, {4000, 4000}, {4010, 4010}},
		"4010,3000-3005,4000": {{3000, 3005}, {4000, 4000}, {4010, 4010}}, // the order of the spec does not matter
		"5000,5001,5002,5004": {{5000, 5002}, {5004, 5004}},
		"3000-3002,3003-3005": {{3000, 3005}}, // neighbours join
		"3000-3005,3002-3004": {{3000, 3005}}, // so do overlaps
		"1-65535":             {{1, 65535}},
	} {
		s, err := ParsePortSet(spec)
		if err != nil {
			t.Fatalf("%q: %v", spec, err)
		}
		if got := s.Ranges(); got == nil || !slices.Equal(got, want) {
			t.Errorf("Ranges of %q = %v, want %v", spec, got, want)
		}
	}

	// Only the members count: a port set to false is not in the set.
	if got := (PortSet{80: false, 81: true, 82: false, 83: true, 84: true}).Ranges(); !slices.Equal(got, []PortRange{{81, 81}, {83, 84}}) {
		t.Errorf("a set with false entries = %v", got)
	}

	// The default reserved ports, where 5000-5001, 9000-9001 and 9092-9093 join.
	def, err := ParsePortSet(DefaultReservedPorts)
	if err != nil {
		t.Fatal(err)
	}
	wantDefault := []PortRange{{3000, 3005}, {4000, 4000}, {4010, 4010}, {4200, 4200}, {5000, 5001}, {5432, 5432}, {8080, 8080}, {8600, 8600}, {9000, 9001}, {9092, 9093}, {9200, 9200}, {9600, 9600}}
	if got := def.Ranges(); !slices.Equal(got, wantDefault) {
		t.Errorf("default reserved ranges = %v", got)
	}

	// The ranges are the wire form: from and to, and an empty set is [], not null.
	if b, _ := json.Marshal(PortRange{From: 3000, To: 3005}); string(b) != `{"from":3000,"to":3005}` {
		t.Errorf("range JSON = %s", b)
	}
	if b, _ := json.Marshal(PortSet(nil).Ranges()); string(b) != `[]` {
		t.Errorf("no ranges JSON = %s", b)
	}
}

func TestPortsInUse(t *testing.T) {
	cfg := func(s string) json.RawMessage { return json.RawMessage(s) }
	feeds := []Feed{
		{ID: "pier-ais", Name: "Pier AIS", Driver: "stream", Enabled: true, Config: cfg(`{"transport":{"kind":"udp","address":":10110"},"decoder":{"name":"nmea-ais"}}`)},
		{ID: "site-cot", Name: "Site CoT", Driver: "stream", Config: cfg(`{"transport":{"kind":"tcpserver","address":"0.0.0.0:6000"},"decoder":{"name":"cot"}}`)},
		{ID: "gate", Name: "Gate", Driver: "tcp", Enabled: true, Config: cfg(`{"mode":"listen","port":6001}`)},
		{ID: "gate-udp", Name: "Gate UDP", Driver: "udp", Config: cfg(`{"port":6001}`)},
		// Connections that do not listen hold no port.
		{ID: "relay", Name: "Relay", Driver: "tcp", Enabled: true, Config: cfg(`{"mode":"connect","host":"relay.example","port":6001}`)},
		{ID: "ais-client", Name: "AIS client", Driver: "stream", Enabled: true, Config: cfg(`{"transport":{"kind":"tcpclient","address":"ais.example:4001"},"decoder":{"name":"nmea-ais"}}`)},
		{ID: "broker", Name: "Broker", Driver: "stream", Enabled: true, Config: cfg(`{"transport":{"kind":"mqtt","brokerUrl":"tcp://b:1883","topics":["x/#"]},"decoder":{"name":"json"}}`)},
		{ID: "opensky", Name: "OpenSky", Driver: "opensky", Enabled: true, Config: cfg(`{}`)},
		{ID: "ghost", Name: "Ghost", Driver: "teleport", Enabled: true, Config: cfg(`{"port":6002}`)},
		// A listener without a usable port is refused when it is saved and holds none.
		{ID: "no-port", Name: "No port", Driver: "stream", Config: cfg(`{"transport":{"kind":"udp","address":"localhost"},"decoder":{"name":"nmea-ais"}}`)},
		{ID: "text-port", Name: "Text port", Driver: "udp", Config: cfg(`{"port":"http"}`)},
		{ID: "huge-port", Name: "Huge port", Driver: "udp", Config: cfg(`{"port":70000}`)},
	}
	want := []PortUse{
		{"tcp", 6000, "site-cot", "Site CoT", false}, // a disabled connection keeps its port
		{"tcp", 6001, "gate", "Gate", true},
		{"udp", 6001, "gate-udp", "Gate UDP", false}, // the same number on the other protocol
		{"udp", 10110, "pier-ais", "Pier AIS", true},
	}
	if got := PortsInUse(feeds); !slices.Equal(got, want) {
		t.Errorf("PortsInUse = %+v\nwant %+v", got, want)
	}

	// The order does not depend on the order of the feeds, and a port two
	// feeds claim (stored before the check existed) lists them by id.
	reversed := slices.Clone(feeds)
	slices.Reverse(reversed)
	reversed = append(reversed,
		Feed{ID: "twin-b", Name: "Twin B", Driver: "udp", Config: cfg(`{"port":7000}`)},
		Feed{ID: "twin-a", Name: "Twin A", Driver: "udp", Config: cfg(`{"port":7000}`)})
	wantWithTwins := []PortUse{want[0], want[1], want[2], {"udp", 7000, "twin-a", "Twin A", false}, {"udp", 7000, "twin-b", "Twin B", false}, want[3]}
	if got := PortsInUse(reversed); !slices.Equal(got, wantWithTwins) {
		t.Errorf("PortsInUse of reversed feeds = %+v\nwant %+v", got, wantWithTwins)
	}

	// No listeners: an empty list, which is JSON [], not null.
	for _, none := range [][]Feed{nil, feeds[4:9]} {
		got := PortsInUse(none)
		if b, _ := json.Marshal(got); got == nil || string(b) != `[]` {
			t.Errorf("PortsInUse of %d feeds that do not listen = %s", len(none), b)
		}
	}

	// The wire form.
	if b, _ := json.Marshal(want[3]); string(b) != `{"protocol":"udp","port":10110,"feedId":"pier-ais","feedName":"Pier AIS","enabled":true}` {
		t.Errorf("port use JSON = %s", b)
	}
}

func TestSuggestPort(t *testing.T) {
	taken := []PortUse{
		{"tcp", 6000, "a", "A", true},
		{"tcp", 6001, "b", "B", false}, // a disabled connection keeps its port
		{"udp", 6002, "c", "C", true},
	}
	reserved := PortSet{6003: true, 6004: true}
	for _, c := range []struct {
		what, protocol string
		from, want     int
	}{
		{"a free port is suggested as it is", "tcp", 7000, 7000},
		{"skips ports taken for the protocol, disabled or not", "tcp", 6000, 6002},
		{"a port taken for the other protocol is free", "udp", 6000, 6000},
		{"skips a udp port in use", "udp", 6002, 6005},
		{"skips reserved ports", "tcp", 6003, 6005},
		{"below the floor starts at the floor", "tcp", 80, MinPort},
		{"zero starts at the floor", "udp", 0, MinPort},
		{"negative starts at the floor", "tcp", -5, MinPort},
		{"above the ceiling wraps to the floor", "tcp", MaxPort + 1, MinPort},
		{"the ceiling itself", "tcp", MaxPort, MaxPort},
	} {
		if got := SuggestPort(c.protocol, c.from, taken, reserved); got != c.want {
			t.Errorf("%s: SuggestPort(%s, %d) = %d, want %d", c.what, c.protocol, c.from, got, c.want)
		}
	}

	// Past the ceiling the search wraps to the floor.
	top := []PortUse{{"tcp", MaxPort - 1, "x", "X", true}, {"tcp", MaxPort, "y", "Y", true}}
	if got := SuggestPort("tcp", MaxPort-1, top, nil); got != MinPort {
		t.Errorf("wrapped suggestion = %d, want %d", got, MinPort)
	}

	// Nothing free is 0; one free port is found from anywhere.
	everything := PortSet{}
	var tcpAll []PortUse
	for p := MinPort; p <= MaxPort; p++ {
		everything[p] = true
		tcpAll = append(tcpAll, PortUse{Protocol: "tcp", Port: p})
	}
	if got := SuggestPort("tcp", 6000, nil, everything); got != 0 {
		t.Errorf("every port reserved: %d", got)
	}
	if got := SuggestPort("tcp", 6000, tcpAll, nil); got != 0 {
		t.Errorf("every port taken: %d", got)
	}
	if got := SuggestPort("udp", 6000, tcpAll, nil); got != 6000 {
		t.Errorf("every port taken for tcp, asked for udp: %d", got)
	}
	delete(everything, 40000)
	for _, from := range []int{MinPort, 6000, 40000, 50000, MaxPort} {
		if got := SuggestPort("tcp", from, nil, everything); got != 40000 {
			t.Errorf("one free port, from %d: %d", from, got)
		}
	}
}

// The endpoint and the save are built on the same rules: a port is suggested
// exactly when the port check accepts a listener on it, and what PortsInUse
// lists is what the check treats as used.
func TestSuggestionsAgreeWithThePortCheck(t *testing.T) {
	reserved, err := ParsePortSet("1030-1039,6000-6001,65535")
	if err != nil {
		t.Fatal(err)
	}
	cfg := func(s string) json.RawMessage { return json.RawMessage(s) }
	feeds := []Feed{
		{ID: "a", Driver: "stream", Enabled: true, Config: cfg(`{"transport":{"kind":"udp","address":":1024"},"decoder":{"name":"nmea-ais"}}`)},
		{ID: "b", Driver: "stream", Config: cfg(`{"transport":{"kind":"tcpserver","address":"127.0.0.1:1025"},"decoder":{"name":"cot"}}`)},
		{ID: "c", Driver: "tcp", Enabled: true, Config: cfg(`{"mode":"listen","port":6002}`)},
		{ID: "d", Driver: "udp", Config: cfg(`{"port":6002}`)},
		{ID: "e", Driver: "udp", Enabled: true, Config: cfg(`{"port":10110}`)},
		{ID: "f", Driver: "tcp", Enabled: true, Config: cfg(`{"mode":"connect","host":"h","port":6003}`)},
	}
	taken := PortsInUse(feeds)
	listener := func(protocol string, port int) Feed {
		if protocol == "tcp" {
			return Feed{ID: "new", Driver: "tcp", Config: cfg(`{"mode":"listen","port":` + strconv.Itoa(port) + `}`)}
		}
		return Feed{ID: "new", Driver: "udp", Config: cfg(`{"port":` + strconv.Itoa(port) + `}`)}
	}
	var ports []int
	for _, window := range [][2]int{{1018, 1045}, {5995, 6010}, {10105, 10115}, {65525, 65535}} {
		for p := window[0]; p <= window[1]; p++ {
			ports = append(ports, p)
		}
	}
	for _, protocol := range []string{"tcp", "udp"} {
		for _, p := range ports {
			accepted := len(CheckPorts(listener(protocol, p), feeds, reserved)) == 0
			if suggested := SuggestPort(protocol, p, taken, reserved) == p; suggested != accepted {
				t.Errorf("%s/%d: the check accepts it = %v, it is suggested = %v", protocol, p, accepted, suggested)
			}
		}
		s := SuggestPort(protocol, SuggestFrom, taken, reserved)
		if s < MinPort || s > MaxPort {
			t.Fatalf("%s suggestion %d is outside %d-%d", protocol, s, MinPort, MaxPort)
		}
		if ps := CheckPorts(listener(protocol, s), feeds, reserved); len(ps) != 0 {
			t.Errorf("the suggested %s port %d is refused: %+v", protocol, s, ps)
		}
	}
}
