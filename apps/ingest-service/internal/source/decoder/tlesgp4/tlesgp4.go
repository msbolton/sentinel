// Package tlesgp4 parses CelesTrak element sets, as 3-line TLE text or as
// OMM JSON, and propagates satellites with SGP4 into observations.
package tlesgp4

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	satellite "github.com/joshuaferrara/go-satellite"

	"github.com/sentinel/ingest-service/internal/obs"
)

// Name is the decoder name stamped on observations.
const Name = "tle-sgp4"

const kmsToKnots = 1943.84

// TLE is one parsed element set, ready to propagate.
type TLE struct {
	Name    string
	Line1   string
	Line2   string
	NoradID int       // the real catalog number, which may exceed the line's five columns
	Epoch   time.Time // zero when unknown
	sat     satellite.Satellite
}

// Parse reads CelesTrak 3-line TLE text (name, line 1, line 2).
func Parse(body string) ([]TLE, error) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var out []TLE
	for i := 0; i+2 < len(lines); i += 3 {
		name := strings.TrimSpace(lines[i])
		l1 := strings.TrimSpace(lines[i+1])
		l2 := strings.TrimSpace(lines[i+2])
		if !strings.HasPrefix(l1, "1 ") || !strings.HasPrefix(l2, "2 ") || len(l1) < 7 {
			continue
		}
		norad, err := strconv.Atoi(strings.TrimSpace(l1[2:7]))
		if err != nil {
			continue
		}
		// The library aborts the process on a field it cannot parse, so a
		// malformed set from a remote is skipped rather than handed to it.
		if err := ValidateLines(l1, l2); err != nil {
			continue
		}
		t := TLE{Name: name, Line1: l1, Line2: l2, NoradID: norad, sat: satellite.TLEToSat(l1, l2, satellite.GravityWGS84)}
		t.Epoch, _ = epochFromLine1(l1)
		out = append(out, t)
	}
	if len(out) == 0 && strings.TrimSpace(body) != "" {
		return nil, fmt.Errorf("no TLE entries found")
	}
	return out, nil
}

// New builds a TLE from its lines (for tests and manual entry).
func New(name, line1, line2 string) (TLE, error) {
	set, err := Parse(name + "\n" + line1 + "\n" + line2 + "\n")
	if err != nil {
		return TLE{}, err
	}
	if len(set) != 1 {
		return TLE{}, fmt.Errorf("invalid TLE")
	}
	return set[0], nil
}

// Propagate computes the satellite's observation at t; nil when the orbit
// has decayed or the elements are bad (NaN/Inf).
func Propagate(tle TLE, t time.Time) *obs.Observation {
	t = t.UTC()
	year, month, day := t.Date()
	hour, min, sec := t.Clock()
	pos, vel := satellite.Propagate(tle.sat, year, int(month), day, hour, min, sec)
	if bad(pos.X) || bad(pos.Y) || bad(pos.Z) {
		return nil
	}
	gmst := satellite.GSTimeFromDate(year, int(month), day, hour, min, sec)
	alt, _, latLng := satellite.ECIToLLA(pos, gmst)
	deg := satellite.LatLongDeg(latLng)
	if bad(deg.Latitude) || bad(deg.Longitude) || bad(alt) {
		return nil
	}
	speedKnots := math.Sqrt(vel.X*vel.X+vel.Y*vel.Y+vel.Z*vel.Z) * kmsToKnots
	norad := strconv.Itoa(tle.NoradID)

	o := &obs.Observation{
		Decoder: Name, Source: "CELESTRAK", SourceEntityID: "SAT-" + norad, DisplayName: tle.Name,
		EntityType: obs.EntitySatellite, TrackEnvironment: obs.EnvSpace, SourceTimestamp: t,
		Altitude: obs.Float(alt * 1000), SpeedKnots: obs.Float(speedKnots),
	}
	o.SetPosition(deg.Latitude, deg.Longitude)
	o.AddIdentity(obs.IdentityNoradID, norad)
	o.TLE = elements(tle)
	return o
}

func elements(tle TLE) *obs.TLEData {
	d := &obs.TLEData{NoradID: tle.NoradID, SatName: tle.Name, Line1: tle.Line1, Line2: tle.Line2}
	if len(tle.Line2) >= 69 {
		d.Inclination, _ = strconv.ParseFloat(strings.TrimSpace(tle.Line2[8:16]), 64)
		d.RAAN, _ = strconv.ParseFloat(strings.TrimSpace(tle.Line2[17:25]), 64)
		d.Eccentricity, _ = strconv.ParseFloat("0."+strings.TrimSpace(tle.Line2[26:33]), 64)
		d.ArgOfPerigee, _ = strconv.ParseFloat(strings.TrimSpace(tle.Line2[34:42]), 64)
		d.MeanAnomaly, _ = strconv.ParseFloat(strings.TrimSpace(tle.Line2[43:51]), 64)
		d.MeanMotion, _ = strconv.ParseFloat(strings.TrimSpace(tle.Line2[52:63]), 64)
		if d.MeanMotion > 0 {
			d.Period = 1440.0 / d.MeanMotion
			semiMajorKm := math.Pow(8681663.653/d.MeanMotion, 2.0/3.0)
			d.Apogee = semiMajorKm*(1+d.Eccentricity) - 6371.0
			d.Perigee = semiMajorKm*(1-d.Eccentricity) - 6371.0
		}
	}
	if len(tle.Line1) >= 17 {
		d.IntlDesignator = strings.TrimSpace(tle.Line1[9:17])
	}
	if !tle.Epoch.IsZero() {
		d.Epoch = tle.Epoch.UTC().Format(time.RFC3339Nano)
	}
	return d
}

func bad(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }
