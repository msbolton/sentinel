package drivers

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
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
		"stream7":    `{"transport": {"kind": "tcpserver", "address": ":4001", "maxConnections": 0, "maxMessagesPerSec": 0}, "decoder": {"name": "json"}}`,
		"stream8":    `{"transport": {"kind": "udp", "address": ":4001", "maxMessagesPerSec": 1000, "maxMessagesPerSecPerSender": 50}, "decoder": {"name": "json"}}`,
		"stream9":    `{"transport": {"kind": "udp", "address": ":4001", "receiveBufferBytes": 8388608}, "decoder": {"name": "json"}}`,
		"httpjson":   `{"url": "https://api.example.com/v2/point/{lat}/{lon}/{radius_nm}", "intervalSec": 15, "requireArea": true, "decoder": {"options": {"records": "ac", "identityType": "ICAO24", "fields": {"id": "hex", "latitude": "lat", "longitude": "lon"}}}}`,
		"tak":        `{"address": "tak.example.org:8089", "siteId": "east", "direction": "both", "tls": {"enabled": true, "serverName": "tak.example.org"}, "heartbeatSec": 30, "tracks": {"staleSec": 120}, "alerts": {"emergencyMinSeverity": "CRITICAL"}, "shapes": {"enabled": true, "refreshSec": 60}, "policy": {"entityTypes": ["AIRCRAFT", "VESSEL"], "feeds": ["opensky"], "boxes": [{"north": 40, "south": 35, "east": -74, "west": -80}], "classificationCeiling": "UNCLASSIFIED", "alerts": {"enabled": true, "minSeverity": "MEDIUM"}}, "outbound": {"perEntityMinSec": 5, "maxEventsPerSec": 100}}`,
		"tcp":        `{"mode": "listen", "port": 6000}`,
		"tcp2":       `{"mode": "connect", "host": "feeds.example", "port": 7000, "format": "cot", "tls": {"enabled": true}}`,
		"tcp3":       `{"mode": "listen", "port": 6000, "maxMessagesPerSec": 1000, "maxMessagesPerSecPerSender": 50}`,
		"udp":        `{"port": 10110, "format": "nmea-ais"}`,
		"udp2":       `{"port": 10111, "encoding": "json", "allow": ["10.0.0.0/8", "192.0.2.7"], "maxMessageBytes": 1500, "maxMessagesPerSec": 0}`,
		"udp3":       `{"port": 10112, "maxMessagesPerSec": 1000, "maxMessagesPerSecPerSender": 50}`,
		"udp4":       `{"port": 10113, "receiveBufferBytes": 4194304}`,
		"udp5":       `{"port": 10114, "receiveBufferBytes": 0}`,
		"udp6":       `{"port": 10115, "receiveBufferBytes": 67108864}`,
		"empty":      ``,
	}
	for driver, cfg := range cases {
		d := driver
		switch d {
		case "empty":
			d = "intelsky"
		case "stream2", "stream3", "stream4", "stream5", "stream6", "stream7", "stream8", "stream9":
			d = "stream"
		case "celestrak2":
			d = "celestrak"
		case "tcp2", "tcp3":
			d = "tcp"
		case "udp2", "udp3", "udp4", "udp5", "udp6":
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
		{"stream", `{"transport": {"kind": "tcpserver", "address": ":4001", "maxMessagesPerSecPerSender": -1}, "decoder": {"name": "json"}}`, "/config/transport/maxMessagesPerSecPerSender"},
		{"stream", `{"transport": {"kind": "udp", "address": ":4001", "maxMessagesPerSecPerSender": "fast"}, "decoder": {"name": "json"}}`, "/config/transport/maxMessagesPerSecPerSender"},
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
		{"udp", `{"port": 10110, "maxMessagesPerSecPerSender": -1}`, "/config/maxMessagesPerSecPerSender"},
		{"udp", `{"port": 10110, "receiveBufferBytes": -1}`, "/config/receiveBufferBytes"},
		{"udp", `{"port": 10110, "receiveBufferBytes": 67108865}`, "/config/receiveBufferBytes"},
		{"udp", `{"port": 10110, "receiveBufferBytes": 1.5}`, "/config/receiveBufferBytes"},
		{"udp", `{"port": 10110, "receiveBufferBytes": "big"}`, "/config/receiveBufferBytes"},
		{"stream", `{"transport": {"kind": "udp", "address": ":4001", "receiveBufferBytes": -1}, "decoder": {"name": "json"}}`, "/config/transport/receiveBufferBytes"},
		{"stream", `{"transport": {"kind": "udp", "address": ":4001", "receiveBufferBytes": 67108865}, "decoder": {"name": "json"}}`, "/config/transport/receiveBufferBytes"},
		{"stream", `{"transport": {"kind": "udp", "address": ":4001", "receiveBufferBytes": "big"}, "decoder": {"name": "json"}}`, "/config/transport/receiveBufferBytes"},
		{"tcp", `{"mode": "listen", "port": 6000, "maxMessagesPerSecPerSender": -1}`, "/config/maxMessagesPerSecPerSender"},
		{"tcp", `{"mode": "listen", "port": 6000, "maxMessagesPerSecPerSender": 1.5}`, "/config/maxMessagesPerSecPerSender"},
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
		{"stream nmea-gps without options", "stream", `{"transport": {"kind": "tcpclient", "address": "gps:2947"}, "decoder": {"name": "nmea-gps"}}`, []string{"/config/decoder/options"}},
		{"stream nmea-gps without a platform id", "stream", `{"transport": {"kind": "tcpclient", "address": "gps:2947"}, "decoder": {"name": "nmea-gps", "options": {}}}`, []string{"/config/decoder/options/sourceEntityId"}},
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

// The nmea-gps decoder refuses to start without the platform its receiver
// reports, so a tcp or udp connection reading it is refused up front, at the
// field. Every other format is left alone.
func TestValidateNMEAGPSNeedsASourceEntityID(t *testing.T) {
	c := Default()
	const optionsPath, idPath = "/config/decoderOptions", "/config/decoderOptions/sourceEntityId"
	listeners := []struct{ name, driver, cfg string }{
		{"tcp listen", "tcp", `"mode": "listen", "port": 6001`},
		{"tcp connect", "tcp", `"mode": "connect", "host": "gps.example", "port": 2947`},
		{"udp", "udp", `"port": 6001`},
	}
	cases := []struct {
		name, rest string
		want       []string // exact problem paths; none means accepted
	}{
		{"an id", `"format": "nmea-gps", "decoderOptions": {"sourceEntityId": "truck-1"}`, nil},
		{"an id with a name and type", `"format": "nmea-gps", "decoderOptions": {"sourceEntityId": "truck-1", "name": "Truck 1", "entityType": "VEHICLE"}`, nil},
		{"no options", `"format": "nmea-gps"`, []string{optionsPath}},
		{"empty options", `"format": "nmea-gps", "decoderOptions": {}`, []string{idPath}},
		{"options without an id", `"format": "nmea-gps", "decoderOptions": {"name": "Truck 1"}`, []string{idPath}},
		{"an empty id", `"format": "nmea-gps", "decoderOptions": {"sourceEntityId": ""}`, []string{idPath}},
		{"a number for the id", `"format": "nmea-gps", "decoderOptions": {"sourceEntityId": 7}`, []string{idPath}},
		{"options that are not an object", `"format": "nmea-gps", "decoderOptions": "truck-1"`, []string{optionsPath}},
		// No other format is asked for an id, with options or without.
		{"no format", ``, nil},
		{"sentinel", `"format": "sentinel"`, nil},
		{"json", `"format": "json"`, nil},
		{"json with a field map", `"format": "json", "decoderOptions": {"fields": {"latitude": "lat", "longitude": "lon"}}`, nil},
		{"cot", `"format": "cot"`, nil},
		{"cot with options", `"format": "cot", "decoderOptions": {"skipSite": "east"}`, nil},
		{"nmea-ais", `"format": "nmea-ais"`, nil},
		{"sbs", `"format": "sbs"`, nil},
		{"link16", `"format": "link16"`, nil},
		{"an id without the format", `"decoderOptions": {"sourceEntityId": "truck-1"}`, nil},
	}
	for _, l := range listeners {
		for _, tc := range cases {
			cfg := "{" + l.cfg
			if tc.rest != "" {
				cfg += ", " + tc.rest
			}
			cfg += "}"
			problems := c.Validate(l.driver, json.RawMessage(cfg))
			if got := problemPaths(problems); !slices.Equal(got, tc.want) {
				t.Errorf("%s, %s: problem paths = %v, want %v (config %s, problems %+v)", l.name, tc.name, got, tc.want, cfg, problems)
			}
		}
	}
}

// The stream driver's nmea-gps decoder refuses to start without the platform
// its receiver reports, as the tcp and udp ones do, so a stream connection
// naming it is refused up front, at the field. The rule is the decoder's, so
// it holds whatever the transport, and every other decoder is left alone.
func TestValidateStreamNMEAGPSNeedsASourceEntityID(t *testing.T) {
	c := Default()
	const optionsPath, idPath = "/config/decoder/options", "/config/decoder/options/sourceEntityId"
	transports := []struct{ name, section string }{
		{"tcpclient", `{"kind": "tcpclient", "address": "gps.example:2947"}`},
		{"tcpserver", `{"kind": "tcpserver", "address": ":4001"}`},
		{"udp", `{"kind": "udp", "address": ":4001"}`},
	}
	cases := []struct {
		name, decoder string
		want          []string // exact problem paths; none means accepted
	}{
		{"an id", `{"name": "nmea-gps", "options": {"sourceEntityId": "truck-1"}}`, nil},
		{"an id with a name and type", `{"name": "nmea-gps", "options": {"sourceEntityId": "truck-1", "name": "Truck 1", "entityType": "VEHICLE"}}`, nil},
		{"no options", `{"name": "nmea-gps"}`, []string{optionsPath}},
		{"empty options", `{"name": "nmea-gps", "options": {}}`, []string{idPath}},
		{"options without an id", `{"name": "nmea-gps", "options": {"name": "Truck 1"}}`, []string{idPath}},
		{"an empty id", `{"name": "nmea-gps", "options": {"sourceEntityId": ""}}`, []string{idPath}},
		{"a number for the id", `{"name": "nmea-gps", "options": {"sourceEntityId": 7}}`, []string{idPath}},
		{"options that are not an object", `{"name": "nmea-gps", "options": "truck-1"}`, []string{optionsPath}},
		// No other decoder is asked for an id, with options or without.
		{"json", `{"name": "json"}`, nil},
		{"json with a field map", `{"name": "json", "options": {"fields": {"latitude": "lat", "longitude": "lon"}}}`, nil},
		{"cot", `{"name": "cot"}`, nil},
		{"nmea-ais", `{"name": "nmea-ais"}`, nil},
		{"sbs", `{"name": "sbs"}`, nil},
		{"beast", `{"name": "beast"}`, nil},
		{"link16", `{"name": "link16"}`, nil},
		{"an id on another decoder", `{"name": "json", "options": {"sourceEntityId": "truck-1"}}`, nil},
		// With no name the decoder is refused for that, and for nothing else.
		{"an id without a decoder name", `{"options": {"sourceEntityId": "truck-1"}}`, []string{"/config/decoder/name"}},
	}
	for _, tr := range transports {
		for _, tc := range cases {
			cfg := `{"transport": ` + tr.section + `, "decoder": ` + tc.decoder + `}`
			problems := c.Validate("stream", json.RawMessage(cfg))
			if got := problemPaths(problems); !slices.Equal(got, tc.want) {
				t.Errorf("%s, %s: problem paths = %v, want %v (config %s, problems %+v)", tr.name, tc.name, got, tc.want, cfg, problems)
			}
		}
	}
}

// The receive buffer's default and ceiling are stated twice: in the schemas the
// form and the API read, and in the constants the transport applies. They must
// say the same.
func TestReceiveBufferSchemasAgreeWithTheTransport(t *testing.T) {
	c := Default()
	for id, path := range map[string][]string{
		"udp":    {"properties", "receiveBufferBytes"},
		"stream": {"properties", "transport", "properties", "receiveBufferBytes"},
	} {
		info, ok := c.Get(id)
		if !ok {
			t.Fatalf("no %s driver", id)
		}
		var node any
		if err := json.Unmarshal(info.ConfigSchema, &node); err != nil {
			t.Fatalf("%s schema: %v", id, err)
		}
		for _, key := range path {
			m, _ := node.(map[string]any)
			if node = m[key]; node == nil {
				t.Fatalf("%s schema has no %s", id, strings.Join(path, "."))
			}
		}
		prop := node.(map[string]any)
		for key, want := range map[string]float64{"minimum": 0, "default": transport.DefaultReceiveBufferBytes, "maximum": transport.MaxReceiveBufferBytes} {
			if got, ok := prop[key].(float64); !ok || got != want {
				t.Errorf("%s schema: receiveBufferBytes %s = %v, want %v, the transport's", id, key, prop[key], want)
			}
		}
	}
}

// secretMeta is one x-secrets entry, as far as the catalog checks it.
type secretMeta struct {
	Title        string         `json:"title"`
	ShowWhen     map[string]any `json:"showWhen"`
	RequiredWhen map[string]any `json:"requiredWhen"`
}

// propertyNode is a schema as far as a secret's conditions care: the nested
// properties a path walks through.
type propertyNode struct {
	Properties map[string]propertyNode `json:"properties"`
}

// has reports whether the slash-separated path names a property, following
// nested properties from this schema.
func (n propertyNode) has(path string) bool {
	for _, name := range strings.Split(path, "/") {
		next, ok := n.Properties[name]
		if !ok {
			return false
		}
		n = next
	}
	return true
}

// A schema describes its secrets under x-secrets, so the form can label each
// one, hint at a reference, and show or require it only while its conditions
// hold. The form reads the entries by secret name and the conditions by
// property path, so a typo in either is silent: an entry for a secret the
// driver does not take is never used, and a condition on a property the schema
// lacks never holds.
func TestSecretMetadataNamesDeclaredSecrets(t *testing.T) {
	// The drivers whose forms take TLS material, and the secrets each must
	// describe, so that a dropped block fails here instead of passing for want
	// of anything to check.
	wantDescribed := map[string][]string{
		"tcp":    {"tlsCert", "tlsKey", "clientCa", "serverCa"},
		"stream": {"tlsCert", "tlsKey", "clientCa", "serverCa"},
		"tak":    {"clientCert", "clientKey", "caCert"},
	}
	for _, info := range Default().List() {
		var schema struct {
			Properties map[string]propertyNode `json:"properties"`
			Secrets    map[string]secretMeta   `json:"x-secrets"`
		}
		if err := json.Unmarshal(info.ConfigSchema, &schema); err != nil {
			t.Errorf("%s: the schema does not decode: %v", info.ID, err)
			continue
		}
		root := propertyNode{Properties: schema.Properties}
		for _, name := range wantDescribed[info.ID] {
			if _, ok := schema.Secrets[name]; !ok {
				t.Errorf("%s: x-secrets has no entry for %s", info.ID, name)
			}
		}
		for name, meta := range schema.Secrets {
			if !slices.Contains(info.SecretFields, name) {
				t.Errorf("%s: x-secrets describes %s, which is not one of the driver's secret fields %v", info.ID, name, info.SecretFields)
			}
			if meta.Title == "" {
				t.Errorf("%s: x-secrets.%s has no title", info.ID, name)
			}
			for when, conditions := range map[string]map[string]any{"showWhen": meta.ShowWhen, "requiredWhen": meta.RequiredWhen} {
				for path := range conditions {
					if !root.has(path) {
						t.Errorf("%s: x-secrets.%s.%s tests %q, which is not a property path in the schema", info.ID, name, when, path)
					}
				}
			}
		}
	}
}
