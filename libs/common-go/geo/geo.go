// Package geo converts between WGS84 geodetic coordinates, Earth-centred
// Earth-fixed (ECEF) and local east-north-up (ENU) frames, turns a sensor's
// polar measurement (azimuth, elevation, range) into a position, and converts
// position uncertainty between covariance and ellipse forms. Angles are
// degrees (azimuth from true north, clockwise), distances metres, altitudes
// metres above the WGS84 ellipsoid.
package geo

import "math"

// WGS84 ellipsoid.
const (
	semiMajorAxis = 6378137.0
	flattening    = 1 / 298.257223563
	semiMinorAxis = semiMajorAxis * (1 - flattening)
	eccSquared    = flattening * (2 - flattening)
)

// LLA is a geodetic position.
type LLA struct{ Lat, Lon, Alt float64 }

// ECEF is an Earth-centred, Earth-fixed position in metres.
type ECEF struct{ X, Y, Z float64 }

// ENU is a vector in a local east-north-up frame, in metres.
type ENU struct{ E, N, U float64 }

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

// ToECEF converts a geodetic position to ECEF.
func ToECEF(p LLA) ECEF {
	sLat, cLat := math.Sincos(rad(p.Lat))
	sLon, cLon := math.Sincos(rad(p.Lon))
	n := semiMajorAxis / math.Sqrt(1-eccSquared*sLat*sLat)
	return ECEF{
		X: (n + p.Alt) * cLat * cLon,
		Y: (n + p.Alt) * cLat * sLon,
		Z: (n*(1-eccSquared) + p.Alt) * sLat,
	}
}

// FromECEF converts an ECEF position to geodetic, iterating the latitude to
// well under a millimetre for any point on or near the Earth.
func FromECEF(c ECEF) LLA {
	p := math.Hypot(c.X, c.Y)
	if p < 1e-9 { // on the polar axis: longitude is undefined
		return LLA{Lat: math.Copysign(90, c.Z), Lon: 0, Alt: math.Abs(c.Z) - semiMinorAxis}
	}
	lat := math.Atan2(c.Z, p*(1-eccSquared))
	for i := 0; i < 8; i++ {
		s := math.Sin(lat)
		n := semiMajorAxis / math.Sqrt(1-eccSquared*s*s)
		h := p/math.Cos(lat) - n
		lat = math.Atan2(c.Z, p*(1-eccSquared*n/(n+h)))
	}
	s, co := math.Sincos(lat)
	// This form of the height stays accurate near the poles, where p/cos(lat) does not.
	h := p*co + c.Z*s - semiMajorAxis*math.Sqrt(1-eccSquared*s*s)
	return LLA{Lat: deg(lat), Lon: deg(math.Atan2(c.Y, c.X)), Alt: h}
}

// ENUOf is target's position in the east-north-up frame at origin.
func ENUOf(origin LLA, target ECEF) ENU {
	o := ToECEF(origin)
	dx, dy, dz := target.X-o.X, target.Y-o.Y, target.Z-o.Z
	sLat, cLat := math.Sincos(rad(origin.Lat))
	sLon, cLon := math.Sincos(rad(origin.Lon))
	return ENU{
		E: -sLon*dx + cLon*dy,
		N: -sLat*cLon*dx - sLat*sLon*dy + cLat*dz,
		U: cLat*cLon*dx + cLat*sLon*dy + sLat*dz,
	}
}

// ECEFOf is the ECEF position of the point v away from origin in its
// east-north-up frame.
func ECEFOf(origin LLA, v ENU) ECEF {
	o := ToECEF(origin)
	sLat, cLat := math.Sincos(rad(origin.Lat))
	sLon, cLon := math.Sincos(rad(origin.Lon))
	return ECEF{
		X: o.X - sLon*v.E - sLat*cLon*v.N + cLat*cLon*v.U,
		Y: o.Y + cLon*v.E - sLat*sLon*v.N + cLat*sLon*v.U,
		Z: o.Z + cLat*v.N + sLat*v.U,
	}
}

// AERToENU is the east-north-up vector of a polar measurement.
func AERToENU(azDeg, elDeg, rangeM float64) ENU {
	sAz, cAz := math.Sincos(rad(azDeg))
	sEl, cEl := math.Sincos(rad(elDeg))
	return ENU{E: rangeM * cEl * sAz, N: rangeM * cEl * cAz, U: rangeM * sEl}
}

// AERToLLA is the position a polar measurement from origin points at.
func AERToLLA(origin LLA, azDeg, elDeg, rangeM float64) LLA {
	return FromECEF(ECEFOf(origin, AERToENU(azDeg, elDeg, rangeM)))
}

// LLAToAER is the polar measurement from origin to target; azimuth is in
// [0, 360).
func LLAToAER(origin, target LLA) (azDeg, elDeg, rangeM float64) {
	v := ENUOf(origin, ToECEF(target))
	rangeM = math.Sqrt(v.E*v.E + v.N*v.N + v.U*v.U)
	azDeg = deg(math.Atan2(v.E, v.N))
	if azDeg < 0 {
		azDeg += 360
	}
	if azDeg >= 360 { // a tiny negative angle rounds up to exactly 360
		azDeg -= 360
	}
	elDeg = deg(math.Atan2(v.U, math.Hypot(v.E, v.N)))
	return azDeg, elDeg, rangeM
}

// AERToLLAAtAltitude is the point at slant range rangeM along azimuth azDeg
// from origin whose altitude is alt, for a measurement with no elevation. It
// solves the elevation by iteration, so the Earth's curvature is accounted
// for. ok is false when no elevation reaches that altitude at that range, or
// the range is negative. ok means the altitude is geometrically reachable at
// that slant range, not that the point is in line of sight (it may be over
// the horizon).
func AERToLLAAtAltitude(origin LLA, azDeg, rangeM, alt float64) (p LLA, elDeg float64, ok bool) {
	if rangeM < 0 {
		return LLA{}, 0, false
	}
	if rangeM == 0 {
		return origin, 0, alt == origin.Alt
	}
	sinEl := (alt - origin.Alt) / rangeM
	if sinEl < -1 || sinEl > 1 {
		return LLA{}, 0, false
	}
	elDeg = deg(math.Asin(sinEl))
	for i := 0; i < 10; i++ {
		p = AERToLLA(origin, azDeg, elDeg, rangeM)
		miss := alt - p.Alt
		if math.Abs(miss) < 0.01 {
			return p, elDeg, true
		}
		// Raising the elevation by Δ (radians) raises the point by about range·Δ.
		elDeg = math.Max(-90, math.Min(90, elDeg+deg(miss/rangeM)))
	}
	p = AERToLLA(origin, azDeg, elDeg, rangeM)
	return p, elDeg, math.Abs(alt-p.Alt) < 1
}
