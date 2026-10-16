package classification

import (
	"reflect"
	"testing"
)

func TestRankAndNormalize(t *testing.T) {
	cases := map[string]int{"": 0, "bogus": 0, "unclassified": 0, "CONFIDENTIAL": 1, " secret ": 2, "TOP_SECRET": 3}
	for in, want := range cases {
		if got := Rank(in); got != want {
			t.Errorf("Rank(%q) = %d, want %d", in, got, want)
		}
		if got := Normalize(in); got != Levels[want] {
			t.Errorf("Normalize(%q) = %q", in, got)
		}
	}
	if Valid("bogus") || !Valid("secret") {
		t.Error("Valid")
	}
	if Min("TOP_SECRET", "secret") != Secret || Min("", "SECRET") != Unclassified {
		t.Error("Min")
	}
}

func TestAtOrBelowAndIntersect(t *testing.T) {
	if got := AtOrBelow("SECRET"); !reflect.DeepEqual(got, []string{Unclassified, Confidential, Secret}) {
		t.Errorf("AtOrBelow = %v", got)
	}
	if got := Intersect(nil, "CONFIDENTIAL"); !reflect.DeepEqual(got, []string{Unclassified, Confidential}) {
		t.Errorf("Intersect(nil) = %v", got)
	}
	if got := Intersect([]string{"secret", "UNCLASSIFIED", "junk"}, "CONFIDENTIAL"); !reflect.DeepEqual(got, []string{Unclassified}) {
		t.Errorf("Intersect(mixed) = %v", got)
	}
	if got := Intersect([]string{"TOP_SECRET"}, "UNCLASSIFIED"); !reflect.DeepEqual(got, []string{"__NONE__"}) {
		t.Errorf("Intersect(all above) = %v", got)
	}
}
