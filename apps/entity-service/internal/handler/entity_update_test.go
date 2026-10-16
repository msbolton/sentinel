package handler

import (
	"testing"
)

func strp(s string) *string { return &s }

func TestValidateUpdate(t *testing.T) {
	cases := []struct {
		name    string
		req     updateEntityRequest
		changed []string
		problem bool
	}{
		{"nothing", updateEntityRequest{}, nil, true},
		{"empty name", updateEntityRequest{Name: strp("  ")}, nil, true},
		{"bad level", updateEntityRequest{Classification: strp("ULTRA")}, nil, true},
		{"name and level normalised", updateEntityRequest{Name: strp(" UAL1 "), Classification: strp(" secret ")}, []string{"name", "classification"}, false},
		{"affiliations and symbol", updateEntityRequest{Affiliations: []string{}, MilStd2525dSymbol: strp("SFAPMF----")}, []string{"affiliations", "milStd2525dSymbol"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed, problem := validateUpdate(&tc.req)
			if (problem != "") != tc.problem {
				t.Fatalf("problem = %q, want problem=%v", problem, tc.problem)
			}
			if tc.problem {
				return
			}
			if len(changed) != len(tc.changed) {
				t.Fatalf("changed = %v, want %v", changed, tc.changed)
			}
			for i := range changed {
				if changed[i] != tc.changed[i] {
					t.Fatalf("changed = %v, want %v", changed, tc.changed)
				}
			}
			if tc.req.Name != nil && *tc.req.Name != "UAL1" {
				t.Errorf("name not trimmed: %q", *tc.req.Name)
			}
			if tc.req.Classification != nil && *tc.req.Classification != "SECRET" {
				t.Errorf("classification not normalised: %q", *tc.req.Classification)
			}
		})
	}
}
