package registry

import (
	"encoding/json"
	"strings"
	"testing"

	_ "github.com/sentinel/ingest-service/internal/source/all"
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
	// Connections that do not listen are not checked.
	client := Feed{ID: "c", Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"tcpclient","address":"h:80"},"decoder":{"name":"sbs"}}`)}
	if ps := CheckPorts(client, nil, reserved); len(ps) != 0 {
		t.Errorf("client: %s", msg(ps))
	}
}

func TestCheckAllow(t *testing.T) {
	bad := Feed{Driver: "udp", Config: json.RawMessage(`{"port":6000,"allow":["10.0.0.0/8","10.0.0.0/33"]}`)}
	if ps := CheckAllow(bad); len(ps) != 1 || ps[0].Path != "/config/allow" {
		t.Errorf("bad CIDR: %+v", ps)
	}
	nested := Feed{Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"udp","address":":6000","allow":["nope"]}}`)}
	if ps := CheckAllow(nested); len(ps) != 1 || ps[0].Path != "/config/transport/allow" {
		t.Errorf("stream allow: %+v", ps)
	}
	if ps := CheckAllow(Feed{Driver: "tcp", Config: json.RawMessage(`{"mode":"listen","port":6000,"allow":["192.168.1.20","2001:db8::/32"]}`)}); len(ps) != 0 {
		t.Errorf("valid list: %+v", ps)
	}
}
