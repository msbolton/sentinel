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
		got := CheckKeyCase(json.RawMessage(c.config), KeyScope{})
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

// An opaque member is data the driver passes on, so its keys are the remote
// API's and case-sensitive; header names are case-insensitive, so two that
// differ only in case are refused, with a message that says so; an object that
// is decoded into fields is checked wherever it is, decoder options included.
func TestCheckKeyCaseScope(t *testing.T) {
	scope := KeyScope{Opaque: []string{"/body"}, HeaderMaps: []string{"/headers"}}
	cases := []struct {
		name, config string
		want         []Problem
	}{
		{"a body with Query and query", `{"url":"u","body":{"Query":"a","query":"b"}}`, nil},
		{"a body, deeply", `{"body":{"a":[{"X":1,"x":2}],"b":{"K":1,"k":2}}}`, nil},
		{"a body spelled Body", `{"Body":{"Query":"a","query":"b"}}`, nil},
		{"a body that is a string", `{"body":"{\"Query\":1,\"query\":2}"}`, nil},
		{"members after a body are still checked", `{"body":{"Q":1,"q":2},"decoder":{"Options":1,"options":2}}`,
			[]Problem{{Path: "/config/decoder", Message: "has keys that differ only in case: Options, options"}}},
		{"headers", `{"headers":{"X-Foo":"a","x-foo":"b","Accept":"c"}}`,
			[]Problem{{Path: "/config/headers", Message: "has header names that differ only in case: X-Foo, x-foo (HTTP header names are case-insensitive)"}}},
		{"headers spelled Headers", `{"Headers":{"X-Foo":"a","X-FOO":"b"}}`,
			[]Problem{{Path: "/config/Headers", Message: "has header names that differ only in case: X-FOO, X-Foo (HTTP header names are case-insensitive)"}}},
		{"headers that differ", `{"headers":{"X-Foo":"a","X-Bar":"b"}}`, nil},
		{"a struct-decoded object still gets the decoder message", `{"decoder":{"options":{"fields":{"Latitude":"a","latitude":"b"}}}}`,
			[]Problem{{Path: "/config/decoder/options/fields", Message: "has keys that differ only in case: Latitude, latitude"}}},
		{"a pointer is exact, not a prefix", `{"bodyExtra":{"K":1,"k":2}}`,
			[]Problem{{Path: "/config/bodyExtra", Message: "has keys that differ only in case: K, k"}}},
	}
	for _, c := range cases {
		got := CheckKeyCase(json.RawMessage(c.config), scope)
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

// The httpjson driver declares its body opaque and its headers a header map,
// so the validator applies the scope without being told about the driver.
func TestValidateKeyCaseScopeOfHTTPJSON(t *testing.T) {
	feed := func(extra string) *Feed {
		return &Feed{ID: "h", Name: "h", Driver: "httpjson", Config: json.RawMessage(
			`{"url":"https://api.example/v1","method":"POST",` + extra + `"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`)}
	}
	if err := newValidator().Validate(feed(`"body":{"Query":"a","query":"b"},`)); err != nil {
		t.Fatalf("a body with keys that differ in case: %v", err)
	}
	err := newValidator().Validate(feed(`"headers":{"X-Foo":"a","x-foo":"b"},`))
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Path != "/config/headers" ||
		ve.Problems[0].Message != "has header names that differ only in case: X-Foo, x-foo (HTTP header names are case-insensitive)" {
		t.Fatalf("headers: %v", err)
	}
}
