package registry

import (
	"encoding/json"
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
