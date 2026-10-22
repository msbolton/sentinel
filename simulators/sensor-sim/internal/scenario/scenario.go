// Package scenario is the simulated world the sensors observe: a set of
// targets moving along routes. Truth is a pure function of the wall clock and
// the scenario, so a radar process and a camera process that load the same
// scenario see the same targets at the same place without talking to each
// other.
package scenario

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/go-common/terrain"
)

// KtsToMps converts knots to metres per second.
const KtsToMps = 1852.0 / 3600.0

// Target kinds.
const (
	KindVehicle = "vehicle"
	KindDrone   = "drone"
	KindPerson  = "person"
)

// Scenario is the shared truth.
type Scenario struct {
	// Epoch is when every target is at the start of its route. Share one value
	// between sensors. When zero, TruthAt uses the start of the current UTC
	// day, which two processes started on the same day agree on.
	Epoch   time.Time `yaml:"epoch"`
	Targets []Target  `yaml:"targets"`
}

// Target is one simulated object moving along a route at constant speed.
type Target struct {
	ID          string  `yaml:"id"`
	Kind        string  `yaml:"kind"` // vehicle, drone or person
	DisplayName string  `yaml:"displayName"`
	SpeedKts    float64 `yaml:"speedKts"`
	// HeightAboveGroundM is held above the terrain: 0 for a ground target.
	HeightAboveGroundM float64 `yaml:"heightAboveGroundM"`
	// Loop closes the route (the last waypoint returns to the first) and keeps
	// going. Without it the target parks at the last waypoint.
	Loop      bool     `yaml:"loop"`
	Waypoints []LatLon `yaml:"waypoints"`
}

// LatLon is a geographic point in degrees.
type LatLon struct {
	Lat float64 `yaml:"lat"`
	Lon float64 `yaml:"lon"`
}

// Truth is where a target really is.
type Truth struct {
	ID, Kind, DisplayName string
	// At is the position, altitude in metres above the WGS84 ellipsoid.
	At geo.LLA
	// VelENU is the velocity in m/s (east, north, up).
	VelENU [3]float64
}

func validLL(p LatLon) bool {
	return p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180 // false for NaN
}

// ValidKind reports whether kind (any case) is a known target kind.
func ValidKind(kind string) bool {
	switch strings.ToLower(kind) {
	case KindVehicle, KindDrone, KindPerson:
		return true
	}
	return false
}

// Validate reports every problem with the scenario, joined into one error.
func (s Scenario) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if len(s.Targets) == 0 {
		add("scenario needs at least one target")
	}
	seen := map[string]bool{}
	for i, t := range s.Targets {
		who := fmt.Sprintf("scenario target %d", i)
		if t.ID != "" {
			who = fmt.Sprintf("scenario target %q", t.ID)
		}
		switch {
		case strings.TrimSpace(t.ID) == "":
			add("%s: id is required", who)
		case seen[t.ID]:
			add("%s: duplicate id", who)
		}
		seen[t.ID] = true
		if !ValidKind(t.Kind) {
			add("%s: kind %q must be vehicle, drone or person", who, t.Kind)
		}
		if len(t.Waypoints) == 0 {
			add("%s: needs at least one waypoint", who)
		}
		for j, w := range t.Waypoints {
			if !validLL(w) {
				add("%s: waypoint %d is not a valid lat/lon", who, j)
			}
		}
		if len(t.Waypoints) > 1 && !(t.SpeedKts > 0 && !math.IsInf(t.SpeedKts, 0)) {
			add("%s: speedKts must be > 0 for a moving target (got %v)", who, t.SpeedKts)
		}
		if !(t.HeightAboveGroundM >= 0 && !math.IsInf(t.HeightAboveGroundM, 0)) {
			add("%s: heightAboveGroundM must be >= 0 (got %v)", who, t.HeightAboveGroundM)
		}
	}
	return errors.Join(errs...)
}

// segment is one straight leg of a route in the tangent plane at its start.
type segment struct {
	from   geo.LLA
	e, n   float64 // east and north extent, metres
	length float64
	start  float64 // distance along the route at the segment's start
}

func route(t Target) (segs []segment, total float64) {
	pts := t.Waypoints
	n := len(pts)
	legs := n - 1
	if t.Loop && n > 1 {
		legs = n
	}
	for i := 0; i < legs; i++ {
		a, b := pts[i], pts[(i+1)%n]
		from := geo.LLA{Lat: a.Lat, Lon: a.Lon}
		v := geo.ENUOf(from, geo.ToECEF(geo.LLA{Lat: b.Lat, Lon: b.Lon}))
		s := segment{from: from, e: v.E, n: v.N, length: math.Hypot(v.E, v.N), start: total}
		segs = append(segs, s)
		total += s.length
	}
	return segs, total
}

// place returns the ground position at distance d along the route and the
// unit heading (east, north) there; moving is false when the target is parked.
func place(t Target, d float64) (lat, lon, ue, un float64, moving bool) {
	segs, total := route(t)
	first := t.Waypoints[0]
	if total == 0 || t.SpeedKts <= 0 {
		return first.Lat, first.Lon, 0, 0, false
	}
	if t.Loop {
		d = math.Mod(d, total)
	} else if d >= total {
		last := t.Waypoints[len(t.Waypoints)-1]
		return last.Lat, last.Lon, 0, 0, false
	}
	if d < 0 {
		d = 0
	}
	for _, s := range segs {
		if s.length == 0 || d > s.start+s.length {
			continue
		}
		f := (d - s.start) / s.length
		p := geo.FromECEF(geo.ECEFOf(s.from, geo.ENU{E: s.e * f, N: s.n * f}))
		return p.Lat, p.Lon, s.e / s.length, s.n / s.length, true
	}
	last := segs[len(segs)-1]
	p := geo.FromECEF(geo.ECEFOf(last.from, geo.ENU{E: last.e, N: last.n}))
	return p.Lat, p.Lon, 0, 0, false
}

// TruthAt returns every target's position and velocity at now, in scenario
// order. A target is at distance speed x (now - Epoch) along its route, so the
// result depends on nothing but the arguments. Altitude is the terrain height
// at the position plus the target's height above ground; the vertical velocity
// is the terrain's change over the next second. t may be nil, which means
// flat ground at 0 m. An error from the terrain is returned.
func (s Scenario) TruthAt(ctx context.Context, t *terrain.Service, now time.Time) ([]Truth, error) {
	epoch := s.Epoch
	if epoch.IsZero() {
		epoch = now.UTC().Truncate(24 * time.Hour)
	}
	elapsed := now.Sub(epoch).Seconds()

	height := func(lat, lon float64) (float64, error) {
		if t == nil {
			return 0, nil
		}
		return t.HeightHAE(ctx, lat, lon)
	}

	out := make([]Truth, 0, len(s.Targets))
	for _, tg := range s.Targets {
		if len(tg.Waypoints) == 0 {
			return nil, fmt.Errorf("scenario target %q has no waypoints", tg.ID)
		}
		speed := tg.SpeedKts * KtsToMps
		lat, lon, ue, un, moving := place(tg, speed*elapsed)
		ground, err := height(lat, lon)
		if err != nil {
			return nil, fmt.Errorf("scenario target %q: %w", tg.ID, err)
		}
		tr := Truth{
			ID: tg.ID, Kind: strings.ToLower(tg.Kind), DisplayName: tg.DisplayName,
			At: geo.LLA{Lat: lat, Lon: lon, Alt: ground + tg.HeightAboveGroundM},
		}
		if moving {
			tr.VelENU = [3]float64{ue * speed, un * speed, 0}
			ahead := geo.FromECEF(geo.ECEFOf(geo.LLA{Lat: lat, Lon: lon}, geo.ENU{E: tr.VelENU[0], N: tr.VelENU[1]}))
			next, err := height(ahead.Lat, ahead.Lon)
			if err != nil {
				return nil, fmt.Errorf("scenario target %q: %w", tg.ID, err)
			}
			tr.VelENU[2] = next - ground
		}
		out = append(out, tr)
	}
	return out, nil
}
