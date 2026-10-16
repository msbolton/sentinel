package drivers

import (
	"encoding/json"
	"strings"
	"testing"
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
	for _, want := range []string{"opensky", "adsblol", "celestrak", "stream"} {
		if !ids[want] {
			t.Errorf("missing driver %s", want)
		}
	}
	if o, _ := c.Get("opensky"); !o.HasCapability(CapabilityArea) || o.AreaTTL == 0 || len(o.SecretFields) != 2 {
		t.Errorf("opensky info = %+v", o)
	}
}

func TestValidateAcceptsSeedConfigs(t *testing.T) {
	c := Default()
	cases := map[string]string{
		"opensky":   `{"intervalSec": 10, "viewportTtlSec": 90, "minPollSpacingSec": 3, "maxAreaSqDeg": 400, "maxIntervalSec": 60, "creditFloor": 200, "staleWarnSec": 120, "staleCriticalSec": 300}`,
		"adsblol":   `{"intervalSec": 10, "staleWarnSec": 120, "staleCriticalSec": 300}`,
		"celestrak": `{"groups": ["active"], "tleRefreshHours": 6, "propagationIntervalSec": 60, "staleWarnSec": 600, "staleCriticalSec": 900}`,
		"stream":    `{"transport": {"kind": "mqtt", "brokerUrl": "tcp://broker:1883", "topics": ["a/#"]}, "decoder": {"name": "json"}}`,
		"empty":     ``,
	}
	for driver, cfg := range cases {
		d := driver
		if d == "empty" {
			d = "adsblol"
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
		{"stream", `{"transport": {"kind": "mqtt"}, "decoder": {"name": "json"}}`, "/config/transport"},
		{"stream", `{"transport": {"kind": "tcpserver", "address": ":4001"}, "decoder": {"name": "xml"}}`, "/config/decoder/name"},
		{"stream", `{"decoder": {"name": "json"}}`, "/config/"},
		{"nope", `{}`, "/driver"},
		{"adsblol", `{not json`, "/config"},
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
