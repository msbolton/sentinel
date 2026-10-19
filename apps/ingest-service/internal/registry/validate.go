package registry

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// Problem is one validation failure, addressed by a JSON-pointer-like path.
type Problem = drivers.Problem

// ValidationError carries every problem found in a feed definition.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.Path + ": " + p.Message
	}
	return "invalid feed: " + strings.Join(msgs, "; ")
}

// reservedIDs are the feed ids that name a route of their own under /feeds.
// The router prefers GET /feeds/ports to GET /feeds/{id}, so a feed with that
// id could never be read.
var reservedIDs = map[string]bool{"ports": true}

// Validator checks feed definitions against the driver catalog.
type Validator struct {
	Catalog *drivers.Catalog
}

// Validate returns a *ValidationError listing every problem, or nil.
func (v *Validator) Validate(f *Feed) error {
	var problems []Problem
	switch {
	case !ValidSlug(f.ID):
		problems = append(problems, Problem{Path: "/id", Message: "must be 1-63 lower-case letters, digits or dashes, starting with a letter or digit"})
	case reservedIDs[f.ID]:
		problems = append(problems, Problem{Path: "/id", Message: "is reserved"})
	}
	if strings.TrimSpace(f.Name) == "" {
		problems = append(problems, Problem{Path: "/name", Message: "is required"})
	}
	info, known := v.Catalog.Get(f.Driver)
	if !known {
		problems = append(problems, Problem{Path: "/driver", Message: fmt.Sprintf("unknown driver %q", f.Driver)})
	} else {
		problems = append(problems, v.Catalog.Validate(f.Driver, f.Config)...)
		problems = append(problems, CheckKeyCase(f.Config)...)
		problems = append(problems, checkSecrets(info, f)...)
		problems = append(problems, checkDirection(info, f)...)
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// checkDirection enforces that a feed's direction, policy and outbound
// tuning fit its driver: config.direction may narrow a "both" driver but
// never widen one, and policy/outbound belong only to connections that send.
func checkDirection(info drivers.Info, f *Feed) []Problem {
	var problems []Problem
	common := f.Common()
	want := driver.Direction(common.Direction)
	drv := info.Dir()
	if !want.Valid() {
		problems = append(problems, Problem{Path: "/config/direction", Message: "must be in, out or both"})
	} else if want != "" && want != drv && !(drv == driver.DirectionBoth && want != driver.DirectionBoth) {
		problems = append(problems, Problem{Path: "/config/direction", Message: fmt.Sprintf("driver %s only runs direction %s", info.ID, drv)})
	}
	if !f.Direction(drv).Outbound() {
		if len(common.Policy) > 0 && string(common.Policy) != "null" {
			problems = append(problems, Problem{Path: "/config/policy", Message: fmt.Sprintf("driver %s does not send; a sharing policy applies only to out or both connections", info.ID)})
		}
		if len(common.Outbound) > 0 && string(common.Outbound) != "null" {
			problems = append(problems, Problem{Path: "/config/outbound", Message: fmt.Sprintf("driver %s does not send; outbound tuning applies only to out or both connections", info.ID)})
		}
	}
	var policy struct {
		Boxes []struct{ North, South, East, West float64 } `json:"boxes"`
	}
	if len(common.Policy) > 0 && json.Unmarshal(common.Policy, &policy) == nil {
		for i, b := range policy.Boxes {
			if b.South > b.North {
				problems = append(problems, Problem{Path: fmt.Sprintf("/config/policy/boxes/%d/south", i), Message: "must not exceed north"})
			}
		}
	}
	return problems
}

// checkSecrets enforces that secrets travel only as references: every
// secretRefs key must be one the driver declares, every value a supported
// reference, and none of the declared secret fields may appear in config.
func checkSecrets(info drivers.Info, f *Feed) []Problem {
	var problems []Problem
	declared := map[string]bool{}
	for _, k := range info.SecretFields {
		declared[k] = true
	}
	for key, ref := range f.SecretRefs {
		if !declared[key] {
			problems = append(problems, Problem{Path: "/secretRefs/" + key, Message: fmt.Sprintf("driver %s does not take this secret", info.ID)})
		}
		if !ValidSecretRef(ref) {
			problems = append(problems, Problem{Path: "/secretRefs/" + key, Message: "must be a reference like env:VARIABLE_NAME or file:/absolute/path, never a plain value"})
		}
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(f.Config, &cfg) == nil {
		for _, k := range info.SecretFields {
			if _, present := cfg[k]; present {
				problems = append(problems, Problem{Path: "/config/" + k, Message: "secrets go in secretRefs as env references, not in config"})
			}
		}
	}
	return problems
}
