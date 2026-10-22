package sentinelobs

import (
	"math"

	"github.com/sentinel/ingest-service/internal/obs"
)

// maxCovariance caps a diagonal variance (m² or (m/s)²) so extreme sender
// values cannot overflow downstream JSON or eigen-decomposition.
const maxCovariance = 1e14

// dropNonFinite removes the NaN and ±Inf a sender's doubles can carry,
// keeping the rest of the report: entity-service stores what it receives
// and cannot render a non-finite number as JSON. An optional value or a
// vector or matrix with any non-finite element becomes absent; a non-finite
// platform detail value becomes 0. Symmetric 3x3 covariances with negative
// diagonal variances are dropped and off-diagonal terms are clamped to
// Cauchy-Schwarz bounds. A polar measurement with a non-finite range or
// azimuth is dropped whole, since a 0 would read as a real bearing; its
// other non-finite optional values become absent. A direction-only one has
// no range to check (a non-finite range_m becomes 0) and is dropped whole
// for a non-finite elevation instead. Latitude and longitude
// are not touched: the position check refuses them first.
func dropNonFinite(o *obs.Observation) {
	for _, p := range []**float64{&o.Altitude, &o.Heading, &o.SpeedKnots, &o.Course, &o.CircularError} {
		if *p != nil && !finite(**p) {
			*p = nil
		}
	}
	for _, p := range []**[3]float64{&o.Velocity, &o.Acceleration, &o.Orientation, &o.Dimensions} {
		if *p != nil && !finite((*p)[:]...) {
			*p = nil
		}
	}
	sanitizeSym3(&o.PosCovariance)
	sanitizeSym3(&o.VelCovariance)
	if o.PosVelCovariance != nil && !finite(o.PosVelCovariance[:]...) {
		o.PosVelCovariance = nil
	}
	if a := o.ADSB; a != nil {
		zeroNonFinite(&a.IndicatedAirSpd, &a.TrueAirSpeed, &a.GroundSpeed, &a.MagneticHeading,
			&a.AltitudeBaro, &a.AltitudeGeom, &a.VerticalRate, &a.RC)
	}
	if a := o.AIS; a != nil {
		zeroNonFinite(&a.Draught, &a.DimensionA, &a.DimensionB, &a.DimensionC, &a.DimensionD, &a.LengthOverall, &a.Beam,
			&a.RateOfTurn, &a.SpeedOverGround, &a.CourseOverGround, &a.TrueHeading)
	}
	if t := o.TLE; t != nil {
		zeroNonFinite(&t.Inclination, &t.Eccentricity, &t.RAAN, &t.ArgOfPerigee, &t.MeanAnomaly, &t.MeanMotion,
			&t.Period, &t.Apogee, &t.Perigee)
	}
	if c := o.CoT; c != nil {
		zeroNonFinite(&c.CE, &c.LE)
	}
	if u := o.UAV; u != nil {
		zeroNonFinite(&u.OperatingFrequency, &u.FrequencyRange)
	}
	if r := o.RF; r != nil {
		zeroNonFinite(&r.FrequencyHz)
		for _, q := range []**float64{&r.BandwidthHz, &r.PriUs, &r.PulseWidthUs, &r.PowerDbm} {
			if *q != nil && !finite(**q) {
				*q = nil
			}
		}
	}
	if r := o.Radar; r != nil {
		for _, q := range []**float64{&r.RangeRateMps, &r.RcsDbsm, &r.SnrDb, &r.TrackQuality, &r.ClassificationConfidence, &r.HeightAboveGroundM} {
			if *q != nil && !finite(**q) {
				*q = nil
			}
		}
	}
	if e := o.EoIr; e != nil {
		for _, q := range []**float64{&e.ClassificationConfidence, &e.HorizontalFovDeg, &e.VerticalFovDeg} {
			if *q != nil && !finite(**q) {
				*q = nil
			}
		}
		if e.BoundingBox != nil && !finite(e.BoundingBox[:]...) {
			e.BoundingBox = nil
		}
	}
	if p := o.Polar; p != nil {
		// A direction-only measurement has no range (a non-finite one
		// becomes 0) and cannot do without its elevation.
		if p.DirectionOnly && !finite(p.RangeM) {
			p.RangeM = 0
		}
		if !finite(p.RangeM, p.AzimuthDeg) || (p.DirectionOnly && p.ElevationDeg != nil && !finite(*p.ElevationDeg)) {
			o.Polar = nil
		} else {
			for _, q := range []**float64{&p.ElevationDeg, &p.RangeRateMps, &p.SigmaRangeM, &p.SigmaAzimuthDeg, &p.SigmaElevationDeg} {
				if *q != nil && !finite(**q) {
					*q = nil
				}
			}
		}
	}
	// Link16Data carries no floats.
}

func sanitizeSym3(p **[6]float64) {
	if *p == nil {
		return
	}
	c := **p
	if !finite(c[:]...) || c[0] < 0 || c[3] < 0 || c[5] < 0 {
		*p = nil
		return
	}
	c[0] = min(c[0], maxCovariance)
	c[3] = min(c[3], maxCovariance)
	c[5] = min(c[5], maxCovariance)
	c[1] = clampAbs(c[1], math.Sqrt(c[0]*c[3]))
	c[2] = clampAbs(c[2], math.Sqrt(c[0]*c[5]))
	c[4] = clampAbs(c[4], math.Sqrt(c[3]*c[5]))
	**p = c
}

func clampAbs(v, limit float64) float64 {
	if v > limit {
		return limit
	}
	if v < -limit {
		return -limit
	}
	return v
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func zeroNonFinite(ps ...*float64) {
	for _, p := range ps {
		if !finite(*p) {
			*p = 0
		}
	}
}
