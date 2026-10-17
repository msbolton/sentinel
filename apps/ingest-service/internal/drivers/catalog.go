// Package drivers is the catalog the management API serves and the
// registry validates against: every registered driver's identity,
// capabilities and the JSON Schema its configuration must satisfy, plus
// the common options every driver accepts.
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

	"github.com/sentinel/ingest-service/internal/source/driver"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// Capability names, re-exported from the driver package.
const (
	CapabilityArea   = driver.CapabilityArea
	CapabilityPoll   = driver.CapabilityPoll
	CapabilityListen = driver.CapabilityListen
)

// Info describes one driver.
type Info struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Source       string          `json:"source"` // EntitySource label written on observations ("" when the decoder decides)
	Direction    string          `json:"direction"` // in | out | both
	Capabilities []string        `json:"capabilities"`
	ConfigSchema json.RawMessage `json:"configSchema"`
	// SecretFields are secret_refs keys the driver expects; they must never
	// appear in config.
	SecretFields []string `json:"secretFields"`
	// AreaTTL is how long a pushed area stays valid without a refresh.
	AreaTTL time.Duration `json:"-"`
}

// Dir is the driver's direction as a typed value.
func (i Info) Dir() driver.Direction { return driver.Direction(i.Direction).Normalize() }

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

// Default builds the catalog from every registered driver. It panics if a
// schema does not compile, which is a programming error caught by tests.
func Default() *Catalog {
	c, err := Build(driver.All())
	if err != nil {
		panic(err)
	}
	return c
}

// Build compiles a catalog from definitions.
func Build(defs []driver.Definition) (*Catalog, error) {
	compiler := jsonschema.NewCompiler()
	add := func(name string, raw []byte) error {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("schema %s: %w", name, err)
		}
		return compiler.AddResource(schemaURL(name), doc)
	}

	c := &Catalog{byID: map[string]Info{}, schemas: map[string]*jsonschema.Schema{}}
	commonRaw, err := schemaFS.ReadFile("schemas/common.json")
	if err != nil {
		return nil, err
	}
	if err := add("common", commonRaw); err != nil {
		return nil, err
	}
	common, err := compiler.Compile(schemaURL("common"))
	if err != nil {
		return nil, fmt.Errorf("compiling common schema: %w", err)
	}
	c.common = common

	for _, d := range defs {
		raw := d.ConfigSchema
		if len(raw) == 0 {
			raw = json.RawMessage(`{"type":"object"}`)
		}
		if err := add(d.ID, raw); err != nil {
			return nil, err
		}
		sch, err := compiler.Compile(schemaURL(d.ID))
		if err != nil {
			return nil, fmt.Errorf("compiling %s schema: %w", d.ID, err)
		}
		info := Info{ID: d.ID, Name: d.Name, Description: d.Description, Source: d.Source, Direction: string(d.Direction.Normalize()), Capabilities: d.Capabilities, ConfigSchema: raw, SecretFields: d.SecretFields, AreaTTL: d.AreaTTL}
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
func (c *Catalog) Validate(driverID string, config json.RawMessage) []Problem {
	sch, ok := c.schemas[driverID]
	if !ok {
		return []Problem{{Path: "/driver", Message: fmt.Sprintf("unknown driver %q", driverID)}}
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
