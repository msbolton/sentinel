// Package drivers describes the feed drivers the ingest service can run:
// their identity, capabilities, and the JSON Schema their configuration must
// satisfy. The management API serves this catalog so clients can build
// forms, and the registry validates feed configuration against it.
package drivers

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// Capability names a runtime feature a driver supports.
const (
	// CapabilityArea means the driver polls an area of interest pushed by
	// the API gateway (PUT /feeds/{id}/area).
	CapabilityArea = "area"
	// CapabilityPoll means the driver polls a remote API on an interval.
	CapabilityPoll = "poll"
	// CapabilityListen means the driver binds a port and must be pinned to
	// one replica.
	CapabilityListen = "listen"
)

// Info describes one driver.
type Info struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Source       string          `json:"source"` // EntitySource label written on observations
	Capabilities []string        `json:"capabilities"`
	ConfigSchema json.RawMessage `json:"configSchema"`
	// SecretFields are secret_refs keys the driver expects; they must never
	// appear in config.
	SecretFields []string `json:"secretFields"`
	// AreaTTL is how long a pushed area stays valid without a refresh.
	AreaTTL time.Duration `json:"-"`
}

// HasCapability reports whether the driver advertises cap.
func (i Info) HasCapability(cap string) bool {
	for _, c := range i.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// Problem is one validation failure.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Catalog holds the known drivers and their compiled schemas.
type Catalog struct {
	infos   []Info
	byID    map[string]Info
	schemas map[string]*jsonschema.Schema
	common  *jsonschema.Schema
}

var printer = message.NewPrinter(language.English)

// Default builds the catalog of built-in drivers. It panics if an embedded
// schema does not compile, which is a programming error caught by tests.
func Default() *Catalog {
	c, err := build([]Info{
		{
			ID: "opensky", Name: "OpenSky Network", Source: "OPENSKY",
			Description:  "Live ADS-B state vectors from the OpenSky Network, polled for the connected clients' viewports.",
			Capabilities: []string{CapabilityPoll, CapabilityArea},
			SecretFields: []string{"clientId", "clientSecret"},
			AreaTTL:      90 * time.Second,
		},
		{
			ID: "adsblol", Name: "adsb.lol military", Source: "ADSB_LOL",
			Description:  "Military aircraft from the adsb.lol community ADS-B network.",
			Capabilities: []string{CapabilityPoll},
		},
		{
			ID: "celestrak", Name: "CelesTrak satellites", Source: "CELESTRAK",
			Description:  "Satellite positions propagated with SGP4 from CelesTrak two-line elements.",
			Capabilities: []string{CapabilityPoll},
		},
		{
			ID: "stream", Name: "Message stream", Source: "STREAM",
			Description:  "Reports delivered over MQTT, STOMP or a TCP listener in a supported wire format.",
			Capabilities: []string{},
		},
	})
	if err != nil {
		panic(err)
	}
	return c
}

func build(infos []Info) (*Catalog, error) {
	compiler := jsonschema.NewCompiler()
	load := func(name string) (json.RawMessage, error) {
		raw, err := schemaFS.ReadFile("schemas/" + name + ".json")
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}
		if err := compiler.AddResource(schemaURL(name), doc); err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}
		return raw, nil
	}

	c := &Catalog{byID: map[string]Info{}, schemas: map[string]*jsonschema.Schema{}}
	if _, err := load("common"); err != nil {
		return nil, err
	}
	common, err := compiler.Compile(schemaURL("common"))
	if err != nil {
		return nil, fmt.Errorf("compiling common schema: %w", err)
	}
	c.common = common

	for _, info := range infos {
		raw, err := load(info.ID)
		if err != nil {
			return nil, err
		}
		sch, err := compiler.Compile(schemaURL(info.ID))
		if err != nil {
			return nil, fmt.Errorf("compiling %s schema: %w", info.ID, err)
		}
		info.ConfigSchema = raw
		if info.Capabilities == nil {
			info.Capabilities = []string{}
		}
		if info.SecretFields == nil {
			info.SecretFields = []string{}
		}
		c.infos = append(c.infos, info)
		c.byID[info.ID] = info
		c.schemas[info.ID] = sch
	}
	return c, nil
}

func schemaURL(name string) string { return "https://sentinel.local/schemas/" + name + ".json" }

// List returns every driver in catalog order.
func (c *Catalog) List() []Info { return append([]Info(nil), c.infos...) }

// Get looks a driver up by id.
func (c *Catalog) Get(id string) (Info, bool) {
	i, ok := c.byID[id]
	return i, ok
}

// Validate checks config against the driver's schema and the common
// options. An unknown driver is reported as a problem at the root.
func (c *Catalog) Validate(driver string, config json.RawMessage) []Problem {
	sch, ok := c.schemas[driver]
	if !ok {
		return []Problem{{Path: "/driver", Message: fmt.Sprintf("unknown driver %q", driver)}}
	}
	if len(bytes.TrimSpace(config)) == 0 {
		config = json.RawMessage("{}")
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(config))
	if err != nil {
		return []Problem{{Path: "/config", Message: "config is not valid JSON"}}
	}
	var problems []Problem
	for _, s := range []*jsonschema.Schema{sch, c.common} {
		if err := s.Validate(inst); err != nil {
			var ve *jsonschema.ValidationError
			if errors.As(err, &ve) {
				flatten(ve, &problems)
			} else {
				problems = append(problems, Problem{Path: "/config", Message: err.Error()})
			}
		}
	}
	return problems
}

func flatten(ve *jsonschema.ValidationError, out *[]Problem) {
	if len(ve.Causes) == 0 {
		*out = append(*out, Problem{
			Path:    "/config/" + strings.Join(ve.InstanceLocation, "/"),
			Message: ve.ErrorKind.LocalizedString(printer),
		})
		return
	}
	for _, cause := range ve.Causes {
		flatten(cause, out)
	}
}
