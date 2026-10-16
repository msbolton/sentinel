package main

import (
	"encoding/json"
	"testing"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/registry"
)

func TestOpenSkyListenerConfigFromFeed(t *testing.T) {
	base := &config.Config{OpenSkyTokenURL: "https://auth/token"}
	spec := registry.Builtins[0]
	cfg, err := openskyListenerConfig(base, spec, map[string]string{"clientId": "id", "clientSecret": "sec"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenSkyIntervalSec != 10 || !cfg.OpenSkyViewportMode || cfg.OpenSkyViewportTTLSec != 90 || cfg.OpenSkyMaxAreaSqDeg != 400 || cfg.OpenSkyCreditFloor != 200 {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.OpenSkyClientID != "id" || cfg.OpenSkyClientSecret != "sec" || cfg.OpenSkyTokenURL != "https://auth/token" {
		t.Errorf("secrets/token not applied: %+v", cfg)
	}
	if base.OpenSkyClientID != "" {
		t.Error("base config must not be mutated")
	}

	spec.Config = json.RawMessage(`{"viewportMode": false, "bbox": "40,-90,45,-80", "creditFloor": 0, "tokenUrl": "https://other/token"}`)
	cfg, _ = openskyListenerConfig(base, spec, nil)
	if cfg.OpenSkyViewportMode || cfg.OpenSkyBBox != "40,-90,45,-80" || cfg.OpenSkyCreditFloor != 0 || cfg.OpenSkyTokenURL != "https://other/token" || cfg.OpenSkyIntervalSec != 10 {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestCelesTrakAndADSBLolConfig(t *testing.T) {
	base := &config.Config{}
	cfg, err := celestrakListenerConfig(base, registry.Feed{ID: "c", Config: json.RawMessage(`{"groups":["active","stations"],"tleRefreshHours":2}`)})
	if err != nil || cfg.CelesTrakGroups != "active,stations" || cfg.CelesTrakTLERefreshHours != 2 || cfg.CelesTrakPropagationIntervalSec != 60 {
		t.Errorf("celestrak cfg = %+v err %v", cfg, err)
	}
	cfg, err = celestrakListenerConfig(base, registry.Feed{ID: "c"})
	if err != nil || cfg.CelesTrakGroups != "active" {
		t.Errorf("celestrak defaults = %+v err %v", cfg, err)
	}
	cfg, err = adsblolListenerConfig(base, registry.Feed{ID: "a", Config: json.RawMessage(`{"intervalSec": 30}`)})
	if err != nil || cfg.ADSBLolIntervalSec != 30 {
		t.Errorf("adsblol cfg = %+v err %v", cfg, err)
	}
	if _, err := adsblolListenerConfig(base, registry.Feed{ID: "a", Config: json.RawMessage(`{"intervalSec": "x"}`)}); err == nil {
		t.Error("bad config must fail")
	}
}

func TestStreamFactoryRejectsUnknownTransport(t *testing.T) {
	f := builtinFactories(&config.Config{})["stream"]
	_, err := f(registry.Feed{ID: "s", Config: json.RawMessage(`{"transport":{"kind":"carrier-pigeon"},"decoder":{"name":"json"}}`)}, nil, runtimeDeps())
	if err == nil {
		t.Fatal("expected an error for an unknown transport")
	}
}
