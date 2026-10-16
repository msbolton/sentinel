package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/runtime"
	"github.com/sentinel/ingest-service/internal/sources"
)

// Driver factories build today's listeners from a feed row. They copy the
// feed's config into the listener fields of a per-feed config copy so the
// existing listener constructors need no change.

type openskyConfig struct {
	IntervalSec       int     `json:"intervalSec"`
	ViewportMode      *bool   `json:"viewportMode"`
	BBox              string  `json:"bbox"`
	ViewportTTLSec    int     `json:"viewportTtlSec"`
	MinPollSpacingSec int     `json:"minPollSpacingSec"`
	MaxAreaSqDeg      float64 `json:"maxAreaSqDeg"`
	MaxIntervalSec    int     `json:"maxIntervalSec"`
	CreditFloor       *int    `json:"creditFloor"`
	TokenURL          string  `json:"tokenUrl"`
}

type adsblolConfig struct {
	IntervalSec int `json:"intervalSec"`
}

type celestrakConfig struct {
	Groups                 []string `json:"groups"`
	TLERefreshHours        int      `json:"tleRefreshHours"`
	PropagationIntervalSec int      `json:"propagationIntervalSec"`
}

type streamConfig struct {
	Transport struct {
		Kind      string   `json:"kind"`
		BrokerURL string   `json:"brokerUrl"`
		Topics    []string `json:"topics"`
		QoS       int      `json:"qos"`
		Queue     string   `json:"queue"`
		Address   string   `json:"address"`
	} `json:"transport"`
	Decoder struct {
		Name string `json:"name"`
	} `json:"decoder"`
}

func decodeConfig(spec registry.Feed, into any) error {
	if len(spec.Config) == 0 {
		return nil
	}
	if err := json.Unmarshal(spec.Config, into); err != nil {
		return fmt.Errorf("feed %s: decoding config: %w", spec.ID, err)
	}
	return nil
}

func intOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func floatOr(v, def float64) float64 {
	if v > 0 {
		return v
	}
	return def
}

// openskyListenerConfig derives the listener config for one OpenSky feed.
func openskyListenerConfig(base *config.Config, spec registry.Feed, secrets map[string]string) (*config.Config, error) {
	var c openskyConfig
	if err := decodeConfig(spec, &c); err != nil {
		return nil, err
	}
	cfg := *base
	cfg.OpenSkyIntervalSec = intOr(c.IntervalSec, 10)
	cfg.OpenSkyViewportMode = c.ViewportMode == nil || *c.ViewportMode
	cfg.OpenSkyBBox = c.BBox
	cfg.OpenSkyViewportTTLSec = intOr(c.ViewportTTLSec, 90)
	cfg.OpenSkyMinPollSpacingSec = intOr(c.MinPollSpacingSec, 3)
	cfg.OpenSkyMaxAreaSqDeg = floatOr(c.MaxAreaSqDeg, 400)
	cfg.OpenSkyMaxIntervalSec = intOr(c.MaxIntervalSec, 60)
	cfg.OpenSkyCreditFloor = 200
	if c.CreditFloor != nil {
		cfg.OpenSkyCreditFloor = *c.CreditFloor
	}
	if c.TokenURL != "" {
		cfg.OpenSkyTokenURL = c.TokenURL
	}
	cfg.OpenSkyClientID = secrets["clientId"]
	cfg.OpenSkyClientSecret = secrets["clientSecret"]
	return &cfg, nil
}

func adsblolListenerConfig(base *config.Config, spec registry.Feed) (*config.Config, error) {
	var c adsblolConfig
	if err := decodeConfig(spec, &c); err != nil {
		return nil, err
	}
	cfg := *base
	cfg.ADSBLolIntervalSec = intOr(c.IntervalSec, 10)
	return &cfg, nil
}

func celestrakListenerConfig(base *config.Config, spec registry.Feed) (*config.Config, error) {
	var c celestrakConfig
	if err := decodeConfig(spec, &c); err != nil {
		return nil, err
	}
	cfg := *base
	if len(c.Groups) == 0 {
		c.Groups = []string{"active"}
	}
	cfg.CelesTrakGroups = strings.Join(c.Groups, ",")
	cfg.CelesTrakTLERefreshHours = intOr(c.TLERefreshHours, 6)
	cfg.CelesTrakPropagationIntervalSec = intOr(c.PropagationIntervalSec, 60)
	return &cfg, nil
}

// builtinFactories maps driver ids to listener constructors.
func builtinFactories(base *config.Config) map[string]runtime.Factory {
	return map[string]runtime.Factory{
		"opensky": func(spec registry.Feed, secrets map[string]string, deps runtime.Deps) (sources.Listener, error) {
			cfg, err := openskyListenerConfig(base, spec, secrets)
			if err != nil {
				return nil, err
			}
			return sources.NewOpenSkyListener(cfg, deps.Input, deps.Logger, deps.Metrics, deps.OnSuccess, deps.OnError), nil
		},
		"adsblol": func(spec registry.Feed, _ map[string]string, deps runtime.Deps) (sources.Listener, error) {
			cfg, err := adsblolListenerConfig(base, spec)
			if err != nil {
				return nil, err
			}
			return sources.NewADSBLolListener(cfg, deps.Input, deps.Logger, deps.Metrics, deps.OnSuccess, deps.OnError), nil
		},
		"celestrak": func(spec registry.Feed, _ map[string]string, deps runtime.Deps) (sources.Listener, error) {
			cfg, err := celestrakListenerConfig(base, spec)
			if err != nil {
				return nil, err
			}
			return sources.NewCelesTrakListener(cfg, deps.Input, deps.Logger, deps.Metrics, deps.OnSuccess, deps.OnError), nil
		},
		"stream": func(spec registry.Feed, _ map[string]string, deps runtime.Deps) (sources.Listener, error) {
			var c streamConfig
			if err := decodeConfig(spec, &c); err != nil {
				return nil, err
			}
			switch c.Transport.Kind {
			case "mqtt":
				return sources.NewMQTTListener(c.Transport.BrokerURL, strings.Join(c.Transport.Topics, ","), spec.ID, deps.Input, deps.Logger, deps.Metrics), nil
			case "stomp":
				return sources.NewSTOMPListener(c.Transport.BrokerURL, c.Transport.Queue, spec.ID, deps.Input, deps.Logger, deps.Metrics), nil
			case "tcpserver":
				return sources.NewTCPListener(c.Transport.Address, spec.ID, deps.Input, deps.Logger, deps.Metrics), nil
			default:
				return nil, fmt.Errorf("feed %s: unsupported transport %q", spec.ID, c.Transport.Kind)
			}
		},
	}
}
