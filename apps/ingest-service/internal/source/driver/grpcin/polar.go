package grpcin

import (
	"math"
	"slices"
	"time"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/obs"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// origin is where an emplacement measures from at a moment, and the
// platform heading a PLATFORM azimuth is relative to (nil when unknown).
type origin struct {
	at      geo.LLA
	heading *float64
}

// maxPolarRangeM bounds a measured range: past it (about half the Earth's
// circumference) the geometry overflows or means nothing.
const maxPolarRangeM = 2e7

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// badPolar reports a measurement nothing can be made of. It is read from the
// message before Validate, which would otherwise drop it silently and let
// the report through without it.
func badPolar(p *sentinelv1.PolarMeasurement) bool {
	if !finite(p.GetRangeM()) || p.GetRangeM() < 0 || p.GetRangeM() > maxPolarRangeM || !finite(p.GetAzimuthDeg()) {
		return true
	}
	if el := p.ElevationDeg; el != nil && (!finite(*el) || *el < -90 || *el > 90) {
		return true
	}
	if s := p.GetSigma(); s != nil {
		for _, v := range []*float64{s.RangeM, s.AzimuthDeg, s.ElevationDeg} {
			if v != nil && (!finite(*v) || *v <= 0) {
				return true
			}
		}
	}
	return false
}

// altitudeOnly reports a position of exactly 0,0 with an altitude: it carries
// only the target's altitude, for a polar measurement to place, and is no fix.
func altitudeOnly(c *sentinelv1.Coordinate) bool {
	return c != nil && c.Latitude == 0 && c.Longitude == 0 && c.AltitudeMeters != nil
}

// trueAzimuth turns an azimuth into one from true north, in [0, 360). A
// PLATFORM azimuth needs the platform's heading.
func trueAzimuth(az float64, ref string, heading *float64) (float64, bool) {
	if ref == emplacement.AzimuthPlatform {
		if heading == nil {
			return 0, false
		}
		az += *heading
	}
	return math.Mod(math.Mod(az, 360)+360, 360), true
}

// fixedOrigin is a fixed emplacement's stored place.
func fixedOrigin(e emplacement.Emplacement) origin {
	o := origin{at: geo.LLA{Lat: e.Position.Lat, Lon: e.Position.Lon}}
	if e.Altitude != nil {
		o.at.Alt = *e.Altitude
	}
	return o
}

// originOf is where e measured from at time at, or the reason there is none.
func (s *service) originOf(e emplacement.Emplacement, at time.Time) (origin, string) {
	if e.Mobility == emplacement.MobilityFixed && e.Position != nil {
		return fixedOrigin(e), ""
	}
	if e.Mobility == emplacement.MobilityMobile {
		if org, ok := s.d.originAt(e.ID, at); ok {
			return org, ""
		}
	}
	return origin{}, "stale_ownship"
}

// convert sets o's position, altitude and covariance from its polar
// measurement taken from org: with an elevation along it, without one at
// the observation's altitude, else the emplacement's. It returns a reject
// reason, or "".
func convert(o *obs.Observation, e emplacement.Emplacement, org origin) string {
	p := o.Polar
	ref := p.AzimuthRef
	if ref == "" {
		ref = e.AzimuthReference
	}
	az, ok := trueAzimuth(p.AzimuthDeg, ref, org.heading)
	if !ok {
		return "bad_polar"
	}
	var target geo.LLA
	var el float64
	if p.ElevationDeg != nil {
		el = *p.ElevationDeg
		target = geo.AERToLLA(org.at, az, el, p.RangeM)
	} else {
		alt := org.at.Alt
		if o.Altitude != nil {
			alt = *o.Altitude
		}
		t, solved, ok := geo.AERToLLAAtAltitude(org.at, az, p.RangeM, alt)
		if !ok {
			return "bad_polar"
		}
		target, el = t, solved
	}
	if !finite(target.Lat) || !finite(target.Lon) || !finite(target.Alt) {
		return "bad_polar"
	}
	o.SetPosition(target.Lat, target.Lon)
	o.Altitude = obs.Float(target.Alt)
	pick := func(sent, stored *float64) *float64 {
		if sent != nil {
			return sent
		}
		return stored
	}
	sigma := geo.Sigma{
		RangeM:       pick(p.SigmaRangeM, e.DefaultSigma.RangeM),
		AzimuthDeg:   pick(p.SigmaAzimuthDeg, e.DefaultSigma.AzimuthDeg),
		ElevationDeg: pick(p.SigmaElevationDeg, e.DefaultSigma.ElevationDeg),
	}
	// A covariance with a non-finite term is dropped, as sentinelobs does.
	if cov, ok := geo.PolarCovarianceNEU(az, el, p.RangeM, sigma); ok && slices.IndexFunc(cov[:], func(v float64) bool { return !finite(v) }) < 0 {
		o.PosCovariance = &cov
		if o.CircularError == nil || *o.CircularError <= 0 {
			if ce := geo.CEP(cov[0], cov[1], cov[3]); ce > 0 && finite(ce) {
				o.CircularError = &ce
			}
		}
	}
	return ""
}

// inCoverage reports whether target lies in e's coverage as seen from org:
// within its ranges, within half its azimuth width of the boresight, and
// within its elevations. An empty coverage holds everything.
func inCoverage(e emplacement.Emplacement, org origin, target geo.LLA) bool {
	c := e.Coverage
	if c.Empty() {
		return true
	}
	az, el, rng := geo.LLAToAER(org.at, target)
	if rng < c.MinRangeM || (c.MaxRangeM > 0 && rng > c.MaxRangeM) {
		return false
	}
	if c.AzimuthWidthDeg > 0 && c.AzimuthWidthDeg < 360 {
		bore, ok := trueAzimuth(e.BoresightDeg, e.AzimuthReference, org.heading)
		if !ok {
			return false
		}
		if off := math.Abs(math.Mod(az-bore+540, 360) - 180); off > c.AzimuthWidthDeg/2 {
			return false
		}
	}
	if c.ElevationMinDeg != nil && el < *c.ElevationMinDeg {
		return false
	}
	if c.ElevationMaxDeg != nil && el > *c.ElevationMaxDeg {
		return false
	}
	return true
}
