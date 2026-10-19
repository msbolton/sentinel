package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// CheckKeyCase refuses a config in which an object has two keys that differ
// only in case. The drivers decode config with encoding/json, which matches a
// key to a field ignoring case and lets the last spelling win; the checks here
// read it the same way, but jsonb reorders keys, so "the last" is not stable
// and a check and a driver could pick different spellings. A key written
// twice the same way is not a difference, and the problem sits at the object's
// path, like "/config/transport".
func CheckKeyCase(raw json.RawMessage) []Problem {
	if !json.Valid(raw) {
		return nil
	}
	var problems []Problem
	walkKeys(json.NewDecoder(bytes.NewReader(raw)), "/config", &problems)
	return problems
}

// walkKeys reads one JSON value from dec, which must be valid, and records the
// objects in it whose keys collide ignoring case. ptr is the path of the value.
func walkKeys(dec *json.Decoder, ptr string, out *[]Problem) {
	tok, err := dec.Token()
	if err != nil {
		return
	}
	switch tok {
	case json.Delim('['):
		for i := 0; dec.More(); i++ {
			walkKeys(dec, fmt.Sprintf("%s/%d", ptr, i), out)
		}
		_, _ = dec.Token() // ]
	case json.Delim('{'):
		type group struct{ spellings []string }
		var groups []*group
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return
			}
			key, _ := k.(string)
			var g *group
			for _, c := range groups {
				if strings.EqualFold(c.spellings[0], key) {
					g = c
					break
				}
			}
			if g == nil {
				g = &group{}
				groups = append(groups, g)
			}
			if !slices.Contains(g.spellings, key) {
				g.spellings = append(g.spellings, key)
			}
			walkKeys(dec, ptr+"/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(key), out)
		}
		_, _ = dec.Token() // }
		for _, g := range groups {
			if len(g.spellings) > 1 {
				slices.Sort(g.spellings)
				*out = append(*out, Problem{Path: ptr, Message: "has keys that differ only in case: " + strings.Join(g.spellings, ", ")})
			}
		}
	}
}
