package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// KeyScope tells CheckKeyCase which parts of a config are not settings.
// Both lists hold JSON pointers below the config ("/body", "/headers"), whose
// segments match the way the driver finds its fields, ignoring case.
type KeyScope struct {
	// Opaque members are data passed on as it is (an HTTP request body); their
	// keys are case-sensitive and are not looked at.
	Opaque []string
	// HeaderMaps are maps of HTTP header names to values. Header names are
	// case-insensitive, so two that differ only in case are ambiguous.
	HeaderMaps []string
}

// CheckKeyCase refuses a config in which an object has two keys that differ
// only in case. The drivers decode config with encoding/json, which matches a
// key to a field ignoring case and lets the last spelling win; the checks here
// read it the same way, but jsonb reorders keys, so "the last" is not stable
// and a check and a driver could pick different spellings. A key written
// twice the same way is not a difference, and the problem sits at the object's
// path, like "/config/transport". Objects that are not decoded into fields are
// left alone, as scope says: opaque members entirely, header maps with a
// message about header names.
func CheckKeyCase(raw json.RawMessage, scope KeyScope) []Problem {
	if !json.Valid(raw) {
		return nil
	}
	w := walker{scope: scope}
	w.walk(json.NewDecoder(bytes.NewReader(raw)), nil)
	return w.problems
}

type walker struct {
	scope    KeyScope
	problems []Problem
}

// atPointer reports whether the member at segs (below the config) is one of
// the pointers in list.
func atPointer(list []string, segs []string) bool {
	for _, p := range list {
		want := strings.Split(strings.TrimPrefix(p, "/"), "/")
		if len(want) == len(segs) {
			same := true
			for i := range want {
				if !strings.EqualFold(want[i], segs[i]) {
					same = false
					break
				}
			}
			if same {
				return true
			}
		}
	}
	return false
}

// foldKey is a form of s that two strings share exactly when strings.EqualFold
// says they are equal (the comparison encoding/json makes between a key and a
// field name): each rune becomes the smallest of its case orbit.
func foldKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		low := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < low {
				low = f
			}
		}
		b.WriteRune(low)
	}
	return b.String()
}

func pointer(segs []string) string {
	esc := strings.NewReplacer("~", "~0", "/", "~1")
	var b strings.Builder
	b.WriteString("/config")
	for _, s := range segs {
		b.WriteString("/" + esc.Replace(s))
	}
	return b.String()
}

// walk reads one JSON value from dec, which must hold valid JSON, and records
// the objects in it whose keys collide ignoring case. segs is the path of the
// value below the config.
func (w *walker) walk(dec *json.Decoder, segs []string) {
	if atPointer(w.scope.Opaque, segs) {
		var skip json.RawMessage
		_ = dec.Decode(&skip)
		return
	}
	tok, err := dec.Token()
	if err != nil {
		return
	}
	switch tok {
	case json.Delim('['):
		for i := 0; dec.More(); i++ {
			w.walk(dec, append(slices.Clone(segs), fmt.Sprint(i)))
		}
		_, _ = dec.Token() // ]
	case json.Delim('{'):
		spellings := map[string][]string{} // folded key to the distinct spellings
		var order []string                 // folded keys in order of appearance
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return
			}
			key, _ := k.(string)
			f := foldKey(key)
			if _, seen := spellings[f]; !seen {
				order = append(order, f)
			}
			if !slices.Contains(spellings[f], key) {
				spellings[f] = append(spellings[f], key)
			}
			w.walk(dec, append(slices.Clone(segs), key))
		}
		_, _ = dec.Token() // }
		what, note := "keys", ""
		if atPointer(w.scope.HeaderMaps, segs) {
			what, note = "header names", " (HTTP header names are case-insensitive)"
		}
		for _, f := range order {
			if s := spellings[f]; len(s) > 1 {
				slices.Sort(s)
				w.problems = append(w.problems, Problem{Path: pointer(segs), Message: "has " + what + " that differ only in case: " + strings.Join(s, ", ") + note})
			}
		}
	}
}
