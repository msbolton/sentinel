// Package detect holds what the simulated sensors share: the Detector
// interface and the mapping from a scenario kind to protocol values.
package detect

import (
	"context"
	"math"
	"strings"
	"time"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/sensor-sim/internal/scenario"
)

// Detector turns the scenario truth at now into the observations one sensor
// would report. It owns its random generator and is not safe for concurrent
// use. Observations carry no position: the server converts the polar
// measurement into one using the sensor's emplacement.
type Detector interface {
	Detect(ctx context.Context, now time.Time, truth []scenario.Truth) []*sentinelv1.Observation
}

// EntityType is the entity type of a scenario kind.
func EntityType(kind string) sentinelv1.EntityType {
	switch strings.ToLower(kind) {
	case scenario.KindDrone:
		return sentinelv1.EntityType_ENTITY_TYPE_DRONE
	case scenario.KindPerson:
		return sentinelv1.EntityType_ENTITY_TYPE_PERSON
	default:
		return sentinelv1.EntityType_ENTITY_TYPE_VEHICLE
	}
}

// Environment is AIR for a drone and GROUND for everything else.
func Environment(kind string) sentinelv1.TrackEnvironment {
	if strings.EqualFold(kind, scenario.KindDrone) {
		return sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR
	}
	return sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND
}

// AngleDiff is a - b in degrees, wrapped to (-180, 180].
func AngleDiff(a, b float64) float64 {
	d := math.Mod(a-b, 360)
	switch {
	case d > 180:
		d -= 360
	case d <= -180:
		d += 360
	}
	return d
}

// WrapAz wraps an azimuth into [0, 360).
func WrapAz(az float64) float64 {
	az = math.Mod(az, 360)
	if az < 0 {
		az += 360
	}
	if az >= 360 { // a tiny negative value rounds up to exactly 360
		az = 0
	}
	return az
}
