package drivers

import (
	"encoding/json"
	"strings"
	"testing"

	_ "github.com/sentinel/ingest-service/internal/source/all"
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
	for _, want := range []string{"opensky", "adsblol", "celestrak", "stream", "httpjson"} {
		if !ids[want] {
			t.Errorf("missing driver %s", want)
		}
	}
	if o, _ := c.Get("opensky"); !o.HasCapability(CapabilityArea) || o.AreaTTL == 0 || len(o.SecretFields) != 2 {
		t.Errorf("opensky info = %+v", o)
	}
	if a, _ := c.Get("adsblol"); !a.HasCapability(CapabilityArea) {
		t.Errorf("adsblol should take an area: %+v", a)
	}
}

func TestValidateAcceptsSeedConfigs(t *testing.T) {
	c := Default()
	cases := map[string]string{
		"opensky":   `{"intervalSec": 10, "viewportTtlSec": 90, "minPollSpacingSec": 3, "maxAreaSqDeg": 400, "maxIntervalSec": 60, "creditFloor": 200, "staleWarnSec": 120, "staleCriticalSec": 300}`,
		"adsblol":   `{"intervalSec": 10, "staleWarnSec": 120, "staleCriticalSec": 300}`,
		"celestrak": `{"groups": ["active"], "tleRefreshHours": 6, "propagationIntervalSec": 60, "staleWarnSec": 600, "staleCriticalSec": 900}`,
		"stream":    `{"transport": {"kind": "mqtt", "brokerUrl": "tcp://broker:1883", "topics": ["a/#"]}, "decoder": {"name": "json"}}`,
		"stream2":   `{"transport": {"kind": "tcpclient", "address": "readsb:30003"}, "framing": "line", "decoder": {"name": "sbs"}, "backpressure": "drop"}`,
		"stream3":   `{"transport": {"kind": "websocket", "url": "wss://x/y"}, "decoder": {"name": "json", "options": {"records": "ac"}}}`,
		"stream4":   `{"transport": {"kind": "tcpclient", "address": "readsb:30005"}, "framing": "beast", "decoder": {"name": "beast"}}`,
		"stream5":   `{"transport": {"kind": "udp", "address": ":10110"}, "decoder": {"name": "nmea-ais"}, "pinnedReplica": "site-1"}`,
		"httpjson":  `{"url": "https://api.example.com/v2/point/{lat}/{lon}/{radius_nm}", "intervalSec": 15, "requireArea": true, "decoder": {"options": {"records": "ac", "identityType": "ICAO24", "fields": {"id": "hex", "latitude": "lat", "longitude": "lon"}}}}`,
		"tak":       `{"address": "tak.example.org:8089", "siteId": "east", "direction": "both", "tls": {"enabled": true, "serverName": "tak.example.org"}, "heartbeatSec": 30, "tracks": {"staleSec": 120}, "alerts": {"emergencyMinSeverity": "CRITICAL"}, "shapes": {"enabled": true, "refreshSec": 60}, "policy": {"entityTypes": ["AIRCRAFT", "VESSEL"], "feeds": ["opensky"], "boxes": [{"north": 40, "south": 35, "east": -74, "west": -80}], "classificationCeiling": "UNCLASSIFIED", "alerts": {"enabled": true, "minSeverity": "MEDIUM"}}, "outbound": {"perEntityMinSec": 5, "maxEventsPerSec": 100}}`,
		"empty":     ``,
	}
	for driver, cfg := range cases {
		d := driver
		switch d {
		case "empty":
			d = "adsblol"
		case "stream2", "stream3", "stream4", "stream5":
			d = "stream"
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
		{"adsblol", `{"scope": "all"}`, "/config/scope"},
		{"stream", `{"transport": {"kind": "mqtt"}, "decoder": {"name": "json"}}`, "/config/transport"},
		{"stream", `{"transport": {"kind": "tcpserver", "address": ":4001"}, "decoder": {"name": "xml"}}`, "/config/decoder/name"},
		{"stream", `{"transport": {"kind": "ftp", "address": ":4001"}, "decoder": {"name": "sbs"}}`, "/config/transport/kind"},
		{"stream", `{"transport": {"kind": "websocket"}, "decoder": {"name": "json"}}`, "/config/transport"},
		{"stream", `{"decoder": {"name": "json"}}`, "/config/"},
		{"nope", `{}`, "/driver"},
		{"adsblol", `{not json`, "/config"},
		{"tak", `{"siteId": "east"}`, "/config/"},
		{"tak", `{"address": "h:1", "protocol": "protobuf"}`, "/config/protocol"},
		{"tak", `{"address": "h:1", "policy": {"boxes": [{"north": 1, "south": 0, "east": 0, "west": 0, "depth": 1}]}}`, "/config/policy/boxes/0"},
		{"tak", `{"address": "h:1", "policy": {"classificationCeiling": "ULTRA"}}`, "/config/policy/classificationCeiling"},
		{"tak", `{"address": "h:1", "outbound": {"queueMax": 1}}`, "/config/outbound/queueMax"},
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
