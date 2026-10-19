package sentinelobs

import (
	"math"

	"github.com/sentinel/ingest-service/internal/obs"
)

// dropNonFinite removes the NaN and ±Inf a sender's doubles can carry,
// keeping the rest of the report: entity-service stores what it receives
// and cannot render a non-finite number as JSON. An optional value or a
// vector or matrix with any non-finite element becomes absent; a non-finite
// platform detail value becomes 0. A polar measurement with a non-finite
// range or azimuth is dropped whole, since a 0 would read as a real bearing;
// its other non-finite optional values become absent. Latitude and longitude
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
	for _, p := range []**[6]float64{&o.PosCovariance, &o.VelCovariance} {
		if *p != nil && !finite((*p)[:]...) {
			*p = nil
		}
	}
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
	if p := o.Polar; p != nil {
		if !finite(p.RangeM, p.AzimuthDeg) {
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
