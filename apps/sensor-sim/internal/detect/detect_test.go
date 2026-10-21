package detect

import (
	"math"
	"testing"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func TestAngleDiffAndWrap(t *testing.T) {
	tests := []struct{ a, b, diff float64 }{
		{10, 350, 20}, {350, 10, -20}, {90, 90, 0}, {180, 0, 180}, {725, 5, 0}, {0, 270, 90},
	}
	for _, tc := range tests {
		if got := AngleDiff(tc.a, tc.b); math.Abs(got-tc.diff) > 1e-9 {
			t.Errorf("AngleDiff(%v,%v) = %v, want %v", tc.a, tc.b, got, tc.diff)
		}
	}
	for in, want := range map[float64]float64{-10: 350, 360: 0, 365: 5, 0: 0, 359.5: 359.5} {
		if got := WrapAz(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("WrapAz(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestKindMapping(t *testing.T) {
	tests := []struct {
		kind string
		typ  sentinelv1.EntityType
		env  sentinelv1.TrackEnvironment
	}{
		{"vehicle", sentinelv1.EntityType_ENTITY_TYPE_VEHICLE, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND},
		{"person", sentinelv1.EntityType_ENTITY_TYPE_PERSON, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND},
		{"drone", sentinelv1.EntityType_ENTITY_TYPE_DRONE, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR},
	}
	for _, tc := range tests {
		if got := EntityType(tc.kind); got != tc.typ {
			t.Errorf("EntityType(%s) = %v", tc.kind, got)
		}
		if got := Environment(tc.kind); got != tc.env {
			t.Errorf("Environment(%s) = %v", tc.kind, got)
		}
	}
}
