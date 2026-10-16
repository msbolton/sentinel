package decoder

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSkipErrors(t *testing.T) {
	err := Skip("no_position")
	if !errors.Is(err, ErrSkip) {
		t.Fatal("Skip must satisfy ErrSkip")
	}
	if SkipReason(err) != "no_position" || SkipReason(ErrSkip) != "no_observation" || SkipReason(errors.New("x")) != "" {
		t.Errorf("SkipReason wrong: %q %q %q", SkipReason(err), SkipReason(ErrSkip), SkipReason(errors.New("x")))
	}
}

func TestRegistry(t *testing.T) {
	Register(Definition{Name: "test-x", New: func(json.RawMessage) (Decoder, error) { return Stateless(nil), nil }})
	if _, ok := Lookup("test-x"); !ok {
		t.Fatal("registered decoder not found")
	}
	if _, err := New("nope", nil); err == nil {
		t.Error("unknown decoder must fail")
	}
	found := false
	for _, n := range Names() {
		if n == "test-x" {
			found = true
		}
	}
	if !found {
		t.Error("Names must list registered decoders")
	}
}
