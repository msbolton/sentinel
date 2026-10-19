package registry

import (
	"encoding/json"
	"errors"
	"testing"
)

// The decoder takes the last of two keys that differ only in case, and jsonb
// reorders keys, so the validation and the decoder can pick different
// spellings. A config that has such a pair is refused outright.
func TestCheckKeyCase(t *testing.T) {
	cases := []struct {
		name, config string
		want         []Problem
	}{
		{"none", `{"port":1,"allow":["a"],"transport":{"kind":"udp"}}`, nil},
		{"the same spelling twice is not a case difference", `{"allow":["a"],"allow":["b"]}`, nil},
		{"top level", `{"port":47000,"allow":[],"Allow":["10.0.0.1"]}`,
			[]Problem{{Path: "/config", Message: "has keys that differ only in case: Allow, allow"}}},
		{"nested", `{"transport":{"kind":"udp","ALLOW":[],"allow":[]}}`,
			[]Problem{{Path: "/config/transport", Message: "has keys that differ only in case: ALLOW, allow"}}},
		{"inside an array", `{"list":[{"a":1},{"b":1,"B":2}]}`,
			[]Problem{{Path: "/config/list/1", Message: "has keys that differ only in case: B, b"}}},
		{"a repeated spelling is listed once", `{"x":1,"X":2,"x":3,"xX":4}`,
			[]Problem{{Path: "/config", Message: "has keys that differ only in case: X, x"}}},
		{"two groups", `{"a":1,"A":2,"b":3,"B":4}`, []Problem{
			{Path: "/config", Message: "has keys that differ only in case: A, a"},
			{Path: "/config", Message: "has keys that differ only in case: B, b"}}},
		{"an escaped spelling is the key it spells", `{"allow":[],"` + string(rune(92)) + `u0041llow":[]}`,
			[]Problem{{Path: "/config", Message: "has keys that differ only in case: Allow, allow"}}},
		{"a slash in the key is escaped in the pointer", `{"a/b":{"k":1,"K":2}}`,
			[]Problem{{Path: "/config/a~1b", Message: "has keys that differ only in case: K, k"}}},
		{"not JSON is left to the schema", `{"a":`, nil},
		{"a scalar", `5`, nil},
	}
	for _, c := range cases {
		got := CheckKeyCase(json.RawMessage(c.config))
		if len(got) != len(c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %+v, want %+v", c.name, got[i], c.want[i])
			}
		}
	}
}

func TestValidateRefusesKeysThatDifferOnlyInCase(t *testing.T) {
	f := &Feed{ID: "u", Name: "u", Driver: "udp", Config: json.RawMessage(`{"port":47000,"allow":["10.0.0.0/8"],"Allow":["0.0.0.0/0"]}`)}
	err := newValidator().Validate(f)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	found := false
	for _, p := range ve.Problems {
		if p.Path == "/config" && p.Message == "has keys that differ only in case: Allow, allow" {
			found = true
		}
	}
	if !found {
		t.Errorf("problems %+v", ve.Problems)
	}
}
