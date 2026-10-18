package drivers

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

func TestDefaultCatalogListsDrivers(t *testing.T) {
	c := Default()
	ids := map[string]bool{}
	for _, d := range c.List() {
		ids[d.ID] = true
		if len(d.ConfigSchema) == 0 {
			t.Errorf("%s has no schema", d.ID)
		}
		var sch map[string]any
		if err := json.Unmarshal(d.ConfigSchema, &sch); err != nil {
			t.Errorf("%s schema is not JSON: %v", d.ID, err)
		}
	}
	for _, want := range []string{"opensky", "celestrak", "aisstream", "intelsky", "stream", "httpjson", "tcp", "udp"} {
		if !ids[want] {
			t.Errorf("missing driver %s", want)
		}
	}
	if o, _ := c.Get("celestrak"); !o.HasCapability(CapabilityArea) || !o.HasCapability(CapabilityPoll) || o.AreaTTL == 0 {
		t.Errorf("celestrak info = %+v", o)
	}
	if o, _ := c.Get("opensky"); !o.HasCapability(CapabilityArea) || o.AreaTTL == 0 || len(o.SecretFields) != 2 {
		t.Errorf("opensky info = %+v", o)
	}
	if _, ok := c.Get("adsblol"); ok {
		t.Error("the adsblol driver was removed")
	}
	if i, _ := c.Get("intelsky"); !i.HasCapability(CapabilityPoll) || i.HasCapability(CapabilityArea) || len(i.SecretFields) != 0 {
		t.Errorf("intelsky info = %+v", i)
	}
	if a, _ := c.Get("aisstream"); !a.HasCapability(CapabilityArea) || a.AreaTTL == 0 || len(a.SecretFields) != 1 || a.SecretFields[0] != "apiKey" {
		t.Errorf("aisstream info = %+v", a)
	}
}

func TestValidateAcceptsSeedConfigs(t *testing.T) {
	c := Default()
	cases := map[string]string{
		"opensky":    `{"intervalSec": 10, "viewportTtlSec": 90, "minPollSpacingSec": 3, "maxAreaSqDeg": 400, "maxIntervalSec": 60, "creditFloor": 200, "staleWarnSec": 120, "staleCriticalSec": 300}`,
		"celestrak":  `{"groups": ["active"], "refreshHours": 2, "propagationIntervalSec": 60, "staleWarnSec": 600, "staleCriticalSec": 900}`,
		"celestrak2": `{"catalogNumbers": [25544], "designators": ["1998-067"], "names": ["ISS (ZARYA)"], "special": ["GPZ"], "supplemental": ["iss"], "tleRefreshHours": 6}`,
		"intelsky":   `{"intervalSec": 10, "staleWarnSec": 120, "staleCriticalSec": 300}`,
		"aisstream":  `{"maxAreaSqDeg": 2500, "movingIntervalSec": 15, "stationaryIntervalSec": 180, "staleWarnSec": 600, "staleCriticalSec": 1800}`,
		"aisstream2": `{"watchlistMmsi": ["235100247", "538005120"], "messageTypes": ["PositionReport", "ShipStaticData"], "maxAreaSqDeg": 0, "movingIntervalSec": 0}`,
		"stream":     `{"transport": {"kind": "mqtt", "brokerUrl": "tcp://broker:1883", "topics": ["a/#"]}, "decoder": {"name": "json"}}`,
		"stream2":    `{"transport": {"kind": "tcpclient", "address": "readsb:30003"}, "framing": "line", "decoder": {"name": "sbs"}, "backpressure": "drop"}`,
		"stream3":    `{"transport": {"kind": "websocket", "url": "wss://x/y"}, "decoder": {"name": "json", "options": {"records": "ac"}}}`,
		"stream4":    `{"transport": {"kind": "tcpclient", "address": "readsb:30005"}, "framing": "beast", "decoder": {"name": "beast"}}`,
		"stream5":    `{"transport": {"kind": "udp", "address": ":10110"}, "decoder": {"name": "nmea-ais"}, "pinnedReplica": "site-1"}`,
		"stream6":    `{"transport": {"kind": "tcpserver", "address": ":4001", "allow": ["10.0.0.0/8"], "maxConnections": 20, "maxMessagesPerSec": 500, "tls": {"enabled": true, "requireClientCert": true}}, "decoder": {"name": "json"}}`,
		"httpjson":   `{"url": "https://api.example.com/v2/point/{lat}/{lon}/{radius_nm}", "intervalSec": 15, "requireArea": true, "decoder": {"options": {"records": "ac", "identityType": "ICAO24", "fields": {"id": "hex", "latitude": "lat", "longitude": "lon"}}}}`,
		"tak":        `{"address": "tak.example.org:8089", "siteId": "east", "direction": "both", "tls": {"enabled": true, "serverName": "tak.example.org"}, "heartbeatSec": 30, "tracks": {"staleSec": 120}, "alerts": {"emergencyMinSeverity": "CRITICAL"}, "shapes": {"enabled": true, "refreshSec": 60}, "policy": {"entityTypes": ["AIRCRAFT", "VESSEL"], "feeds": ["opensky"], "boxes": [{"north": 40, "south": 35, "east": -74, "west": -80}], "classificationCeiling": "UNCLASSIFIED", "alerts": {"enabled": true, "minSeverity": "MEDIUM"}}, "outbound": {"perEntityMinSec": 5, "maxEventsPerSec": 100}}`,
		"tcp":        `{"mode": "listen", "port": 6000}`,
		"tcp2":       `{"mode": "connect", "host": "feeds.example", "port": 7000, "format": "cot", "tls": {"enabled": true}}`,
		"udp":        `{"port": 10110, "format": "nmea-ais"}`,
		"udp2":       `{"port": 10111, "encoding": "json", "allow": ["10.0.0.0/8", "192.0.2.7"], "maxMessageBytes": 1500, "maxMessagesPerSec": 0}`,
		"empty":      ``,
	}
	for driver, cfg := range cases {
		d := driver
		switch d {
		case "empty":
			d = "intelsky"
		case "stream2", "stream3", "stream4", "stream5", "stream6":
			d = "stream"
		case "celestrak2":
			d = "celestrak"
		case "tcp2":
			d = "tcp"
		case "udp2":
			d = "udp"
		case "aisstream2":
			d = "aisstream"
		}
		if problems := c.Validate(d, json.RawMessage(cfg)); len(problems) != 0 {
			t.Errorf("%s: unexpected problems %+v", driver, problems)
		}
	}
}

func TestValidateRejectsBadConfigs(t *testing.T) {
	c := Default()
	cases := []struct {
		driver, cfg, wantPath string
	}{
		{"opensky", `{"intervalSec": 0}`, "/config/intervalSec"},
		{"opensky", `{"bbox": "nope"}`, "/config/bbox"},
		{"opensky", `{"staleWarnSec": -1}`, "/config/staleWarnSec"},
		{"celestrak", `{"groups": []}`, "/config/groups"},
		{"celestrak", `{"refreshHours": 1}`, "/config/refreshHours"},
		{"celestrak", `{"special": ["nope"]}`, "/config/special"},
		{"celestrak", `{"catalogNumbers": [0]}`, "/config/catalogNumbers"},
		{"intelsky", `{"intervalSec": 2}`, "/config/intervalSec"},
		{"intelsky", `{"url": "ftp://x"}`, "/config/url"},
		{"aisstream", `{"watchlistMmsi": ["12345"]}`, "/config/watchlistMmsi/0"},
		{"aisstream", `{"messageTypes": ["SafetyBroadcastMessage"]}`, "/config/messageTypes/0"},
		{"aisstream", `{"movingIntervalSec": -1}`, "/config/movingIntervalSec"},
		{"stream", `{"transport": {"kind": "mqtt"}, "decoder": {"name": "json"}}`, "/config/transport/brokerUrl"},
		{"stream", `{"transport": {"kind": "tcpserver", "address": ":4001"}, "decoder": {"name": "xml"}}`, "/config/decoder/name"},
		{"stream", `{"transport": {"kind": "ftp", "address": ":4001"}, "decoder": {"name": "sbs"}}`, "/config/transport/kind"},
		{"stream", `{"transport": {"kind": "websocket"}, "decoder": {"name": "json"}}`, "/config/transport/url"},
		{"stream", `{"decoder": {"name": "json"}}`, "/config/transport"},
		{"stream", `{"transport": {"kind": "tcpserver", "address": ":4001", "maxConnections": -1}, "decoder": {"name": "json"}}`, "/config/transport/maxConnections"},
		{"stream", `{"transport": {"kind": "udp", "address": ":4001", "maxMessagesPerSec": -1}, "decoder": {"name": "json"}}`, "/config/transport/maxMessagesPerSec"},
		{"stream", `{"transport": {"kind": "udp", "address": ":4001", "allow": "oops"}, "decoder": {"name": "json"}}`, "/config/transport/allow"},
		{"stream", `{"transport": {"kind": "tcpserver", "address": ":4001", "tls": {"enabled": true, "requireClientCert": "yes"}}, "decoder": {"name": "json"}}`, "/config/transport/tls/requireClientCert"},
		{"nope", `{}`, "/driver"},
		{"intelsky", `{not json`, "/config"},
		{"tak", `{"siteId": "east"}`, "/config/address"},
		{"tak", `{"address": "h:1", "protocol": "protobuf"}`, "/config/protocol"},
		{"tak", `{"address": "h:1", "policy": {"boxes": [{"north": 1, "south": 0, "east": 0, "west": 0, "depth": 1}]}}`, "/config/policy/boxes/0"},
		{"tak", `{"address": "h:1", "policy": {"classificationCeiling": "ULTRA"}}`, "/config/policy/classificationCeiling"},
		{"tak", `{"address": "h:1", "outbound": {"queueMax": 1}}`, "/config/outbound/queueMax"},
		{"tcp", `{"mode": "listen"}`, "/config/port"},
		{"tcp", `{"mode": "listen", "port": 80}`, "/config/port"},
		{"tcp", `{"mode": "connect", "port": 7000}`, "/config/host"},
		{"tcp", `{"mode": "listen", "port": 6000, "format": "xml"}`, "/config/format"},
		{"tcp", `{"mode": "listen", "port": 6000, "tls": {"enabled": true, "certFile": "/x"}}`, "/config/tls"},
		{"tcp", `{"mode": "listen", "port": 6000, "tls": {"enabled": true, "requireClientCrt": true}}`, "/config/tls"},
		{"udp", `{}`, "/config/port"},
		{"udp", `{"port": 80}`, "/config/port"},
		{"udp", `{"port": 70000}`, "/config/port"},
		{"udp", `{"port": 10110, "format": "beast"}`, "/config/format"},
		{"udp", `{"port": 10110, "allow": "oops"}`, "/config/allow"},
		{"udp", `{"port": 10110, "maxMessageBytes": 70000}`, "/config/maxMessageBytes"},
	}
	for _, tc := range cases {
		problems := c.Validate(tc.driver, json.RawMessage(tc.cfg))
		if len(problems) == 0 {
			t.Errorf("%s %s: expected a problem", tc.driver, tc.cfg)
			continue
		}
		found := false
		for _, p := range problems {
			if strings.HasPrefix(p.Path, tc.wantPath) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s %s: problems %+v lack path %s", tc.driver, tc.cfg, problems, tc.wantPath)
		}
	}
}

// problemPaths lists the paths of problems in sorted order, so a test can
// compare the exact set a config is refused at.
func problemPaths(problems []Problem) []string {
	paths := make([]string, 0, len(problems))
	for _, p := range problems {
		paths = append(paths, p.Path)
	}
	slices.Sort(paths)
	return paths
}

// A missing property is reported at the property itself rather than at the
// object that lacks it, so a form can put the message on the field.
func TestValidateRequiredPointsAtTheField(t *testing.T) {
	c := Default()
	cases := []struct {
		name, driver, cfg string
		want              []string
	}{
		{"tcp listen without a port", "tcp", `{"mode": "listen"}`, []string{"/config/port"}},
		{"tcp connect without a host", "tcp", `{"mode": "connect", "port": 7000}`, []string{"/config/host"}},
		{"tcp without a mode", "tcp", `{}`, []string{"/config/mode"}},
		{"udp without a port", "udp", `{}`, []string{"/config/port"}},
		{"stream without a transport", "stream", `{"decoder": {"name": "json"}}`, []string{"/config/transport"}},
		{"stream without transport or decoder", "stream", `{}`, []string{"/config/decoder", "/config/transport"}},
		{"mqtt without a broker or topics", "stream", `{"transport": {"kind": "mqtt"}, "decoder": {"name": "json"}}`, []string{"/config/transport/brokerUrl", "/config/transport/topics"}},
		{"websocket without a url", "stream", `{"transport": {"kind": "websocket"}, "decoder": {"name": "json"}}`, []string{"/config/transport/url"}},
		{"tak without an address", "tak", `{"siteId": "east"}`, []string{"/config/address"}},
		{"transport and decoder with nothing in them", "stream", `{"transport": {}, "decoder": {}}`, []string{"/config/decoder/name", "/config/transport/kind"}},
		{"sharing box without its corners", "tak", `{"address": "h:1", "policy": {"boxes": [{"north": 1}]}}`, []string{"/config/policy/boxes/0/east", "/config/policy/boxes/0/south", "/config/policy/boxes/0/west"}},
	}
	for _, tc := range cases {
		problems := c.Validate(tc.driver, json.RawMessage(tc.cfg))
		if got := problemPaths(problems); !slices.Equal(got, tc.want) {
			t.Errorf("%s: problem paths = %v, want %v (%+v)", tc.name, got, tc.want, problems)
		}
		for _, p := range problems {
			if p.Message != "is required" {
				t.Errorf("%s: message at %s = %q, want %q", tc.name, p.Path, p.Message, "is required")
			}
		}
	}
}

// Each keyword that names absent properties reports them at the properties,
// whatever depth they sit at, and a name holding "/" or "~" is escaped as
// RFC 6901 says.
func TestValidateRequiredPointsForEveryKeyword(t *testing.T) {
	cases := []struct {
		name, schema, cfg string
		want              []string
	}{
		{"required",
			`{"type": "object", "required": ["a/b", "c~d"], "properties": {"e/f": {"type": "object", "required": ["g~h"]}}}`,
			`{"e/f": {}}`,
			[]string{"/config/a~1b", "/config/c~0d", "/config/e~1f/g~0h"}},
		{"dependentRequired",
			`{"type": "object", "dependentRequired": {"user": ["password", "realm/id"]}, "properties": {"auth": {"type": "object", "dependentRequired": {"user": ["pass~word"]}}}}`,
			`{"user": "u", "auth": {"user": "u"}}`,
			[]string{"/config/auth/pass~0word", "/config/password", "/config/realm~1id"}},
		{"dependencies",
			`{"$schema": "http://json-schema.org/draft-07/schema#", "type": "object", "dependencies": {"user": ["password", "realm/id"]}}`,
			`{"user": "u"}`,
			[]string{"/config/password", "/config/realm~1id"}},
	}
	for _, tc := range cases {
		c, err := Build([]driver.Definition{{ID: "probe", ConfigSchema: json.RawMessage(tc.schema)}})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		problems := c.Validate("probe", json.RawMessage(tc.cfg))
		if got := problemPaths(problems); !slices.Equal(got, tc.want) {
			t.Errorf("%s: problem paths = %v, want %v (%+v)", tc.name, got, tc.want, problems)
		}
		for _, p := range problems {
			if p.Message != "is required" {
				t.Errorf("%s: message at %s = %q, want %q", tc.name, p.Path, p.Message, "is required")
			}
		}
	}
}

// Every problem path is a JSON pointer: the config itself is /config, not
// /config/, and a name holding "/" or "~" is escaped.
func TestValidateProblemPathsAreJSONPointers(t *testing.T) {
	cases := []struct {
		name, schema, cfg, want string
	}{
		{"the config itself", `{"type": "object", "additionalProperties": false}`, `{"x": 1}`, "/config"},
		{"a name holding a slash", `{"type": "object", "properties": {"a/b": {"type": "integer"}}}`, `{"a/b": "x"}`, "/config/a~1b"},
		{"a name holding a tilde", `{"type": "object", "properties": {"a~b": {"type": "object", "properties": {"c/d": {"type": "integer"}}}}}`, `{"a~b": {"c/d": "x"}}`, "/config/a~0b/c~1d"},
	}
	for _, tc := range cases {
		c, err := Build([]driver.Definition{{ID: "probe", ConfigSchema: json.RawMessage(tc.schema)}})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := problemPaths(c.Validate("probe", json.RawMessage(tc.cfg))); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("%s: problem paths = %v, want [%s]", tc.name, got, tc.want)
		}
	}
}
