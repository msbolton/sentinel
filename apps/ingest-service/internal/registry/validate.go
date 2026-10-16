package registry

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/source/transport"
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

// Validator checks feed definitions against the driver catalog.
type Validator struct {
	Catalog *drivers.Catalog
}

// Validate returns a *ValidationError listing every problem, or nil.
func (v *Validator) Validate(f *Feed) error {
	var problems []Problem
	if !ValidSlug(f.ID) {
		problems = append(problems, Problem{Path: "/id", Message: "must be 1-63 lower-case letters, digits or dashes, starting with a letter or digit"})
	}
	if strings.TrimSpace(f.Name) == "" {
		problems = append(problems, Problem{Path: "/name", Message: "is required"})
	}
	info, known := v.Catalog.Get(f.Driver)
	if !known {
		problems = append(problems, Problem{Path: "/driver", Message: fmt.Sprintf("unknown driver %q", f.Driver)})
	} else {
		problems = append(problems, v.Catalog.Validate(f.Driver, f.Config)...)
		problems = append(problems, checkSecrets(info, f)...)
		problems = append(problems, checkPinned(f)...)
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// checkPinned requires pinnedReplica for transports that bind a local
// port: a listening feed can only run where its senders are pointed.
func checkPinned(f *Feed) []Problem {
	if f.Driver != "stream" {
		return nil
	}
	var cfg struct {
		Transport struct {
			Kind string `json:"kind"`
		} `json:"transport"`
		PinnedReplica string `json:"pinnedReplica"`
	}
	if json.Unmarshal(f.Config, &cfg) != nil {
		return nil
	}
	def, ok := transport.Lookup(cfg.Transport.Kind)
	if !ok || !def.Listens || strings.TrimSpace(cfg.PinnedReplica) != "" {
		return nil
	}
	return []Problem{{Path: "/config/pinnedReplica", Message: fmt.Sprintf("transport %s listens on a port and must be pinned to the replica the senders reach (set pinnedReplica)", cfg.Transport.Kind)}}
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
			problems = append(problems, Problem{Path: "/secretRefs/" + key, Message: "must be a reference like env:VARIABLE_NAME, never a plain value"})
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
