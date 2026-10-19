package registry

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/sentinel/ingest-service/internal/drivers"
)

// requiredSecrets lists, sorted, the secrets the driver's schema asks for
// (x-secrets.<name>.requiredWhen) under this config and that secretRefs lacks.
//
// The conditions read as the client's form reads them (schema-form
// conditionsOf and matches), so the form and the server agree: each key is a
// slash-separated path from the config root; its value is a value or a list of
// values, any of which satisfies it; every condition must hold. A value that is
// missing, null or empty text counts as the schema's const or default for that
// path, if it has one. A secret that is present, even empty, is not reported
// here: an empty or malformed reference is reported for what it is.
func requiredSecrets(info drivers.Info, config json.RawMessage, refs map[string]string) []string {
	var schema struct {
		Secrets map[string]struct {
			RequiredWhen map[string]json.RawMessage `json:"requiredWhen"`
		} `json:"x-secrets"`
	}
	if json.Unmarshal(info.ConfigSchema, &schema) != nil || len(schema.Secrets) == 0 {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(config, &root); err != nil {
		return nil // not an object: the schema check says so
	}
	var props map[string]any
	var doc map[string]any
	if json.Unmarshal(info.ConfigSchema, &doc) == nil {
		props, _ = doc["properties"].(map[string]any)
	}
	var missing []string
	for name, meta := range schema.Secrets {
		if len(meta.RequiredWhen) == 0 {
			continue
		}
		if _, present := refs[name]; present {
			continue
		}
		holds := true
		for path, want := range meta.RequiredWhen {
			if !conditionHolds(strings.Split(path, "/"), want, root, props) {
				holds = false
				break
			}
		}
		if holds {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// conditionHolds reports whether the value at path in the config is one of
// want (a value, or a list of them).
func conditionHolds(path []string, want json.RawMessage, root map[string]any, props map[string]any) bool {
	var values []any
	if json.Unmarshal(want, &values) != nil {
		var one any
		if json.Unmarshal(want, &one) != nil {
			return false
		}
		values = []any{one}
	}
	current := valueAt(root, path)
	if s, ok := current.(string); ok {
		current = strings.TrimSpace(s)
	}
	if current == nil || current == "" {
		current = defaultAt(props, path)
	}
	for _, v := range values {
		if reflect.DeepEqual(v, current) {
			return true
		}
	}
	return false
}

// valueAt walks the config along path, ignoring the case of the keys as the
// drivers do; nil when any step is missing.
func valueAt(node any, path []string) any {
	for _, key := range path {
		obj, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		next, found := obj[key]
		if !found {
			for k, v := range obj {
				if strings.EqualFold(k, key) {
					next, found = v, true
					break
				}
			}
		}
		if !found {
			return nil
		}
		node = next
	}
	return node
}

// defaultAt is the const, else the default, the schema gives the property at
// path (through the nested "properties"); nil if it gives neither.
func defaultAt(props map[string]any, path []string) any {
	var prop map[string]any
	for _, key := range path {
		next, ok := props[key].(map[string]any)
		if !ok {
			return nil
		}
		prop = next
		props, _ = next["properties"].(map[string]any)
	}
	if prop == nil {
		return nil
	}
	if c, ok := prop["const"]; ok {
		return c
	}
	return prop["default"]
}
