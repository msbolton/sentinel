package terrain

import (
	"context"
	"fmt"
	"math"

	"github.com/sentinel/go-common/geo"
)

// Line-of-sight settings.
const (
	StepM            = 15.0    // ray-march step along the slant range
	RefineM          = 0.5     // bracket width the crossing is refined to
	VerticalSigM     = 4.0     // standard deviation of a terrain height
	DefaultMaxRangeM = 30000.0 // how far a caller looks when it has no better figure
)

// Hit is where a line of sight first meets the ground. When Found, At is the
// crossing with its altitude set to the ground height there, and RangeM the
// slant range to it from the origin.
type Hit struct {
	At     geo.LLA
	RangeM float64
	Found  bool
}

// ray is a straight line in ECEF: the origin and a unit direction.
type ray struct{ o, d geo.ECEF }

func newRay(origin geo.LLA, azDeg, elDeg float64) ray {
	o := geo.ToECEF(origin)
	p := geo.ECEFOf(origin, geo.AERToENU(azDeg, elDeg, 1))
	return ray{o: o, d: geo.ECEF{X: p.X - o.X, Y: p.Y - o.Y, Z: p.Z - o.Z}}
}

// at is the point rangeM along the ray; the same point geo.AERToLLA gives,
// without redoing the origin's frame at every step.
func (r ray) at(rangeM float64) geo.LLA {
	return geo.FromECEF(geo.ECEF{X: r.o.X + rangeM*r.d.X, Y: r.o.Y + rangeM*r.d.Y, Z: r.o.Z + rangeM*r.d.Z})
}

// clearance is the point rangeM along the ray and its height above the
// ground there (negative below).
func (s *Service) clearance(ctx context.Context, r ray, rangeM float64) (geo.LLA, float64, error) {
	p := r.at(rangeM)
	g, err := s.HeightHAE(ctx, p.Lat, p.Lon)
	return p, p.Alt - g, err
}

func finite(v ...float64) bool {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}

// Intersect follows the line of sight from origin along azimuth azDeg and
// elevation elDeg for up to maxRangeM of slant range and returns where it
// first meets the ground. It marches in StepM steps until the ray is at or
// below the ground, then bisects that step down to RefineM and interpolates
// within it. A ray that stays above the ground returns Found false and a nil
// error; an origin below the ground under it is a hit at range 0. Any
// error from HeightHAE, ErrUnavailable included, is returned, as is ctx's
// error once it is done. Curvature is included: the ray is straight in ECEF.
// The ground is only sampled every StepM along the ray, so a feature
// narrower than that between two samples (a mast, a thin wall) can be
// stepped over.
func (s *Service) Intersect(ctx context.Context, origin geo.LLA, azDeg, elDeg, maxRangeM float64) (Hit, error) {
	if !finite(origin.Lat, origin.Lon, origin.Alt, azDeg, elDeg, maxRangeM) || maxRangeM < 0 {
		return Hit{}, fmt.Errorf("terrain: no line of sight from (%g, %g, %g) at azimuth %g, elevation %g, out to %g m",
			origin.Lat, origin.Lon, origin.Alt, azDeg, elDeg, maxRangeM)
	}
	if err := ctx.Err(); err != nil {
		return Hit{}, err
	}
	g, err := s.HeightHAE(ctx, origin.Lat, origin.Lon)
	if err != nil {
		return Hit{}, err
	}
	if origin.Alt < g {
		at := origin
		at.Alt = g
		return Hit{At: at, RangeM: 0, Found: true}, nil
	}

	r := newRay(origin, azDeg, elDeg)
	lo, cLo := 0.0, origin.Alt-g
	for k := 1; lo < maxRangeM; k++ {
		if err := ctx.Err(); err != nil {
			return Hit{}, err
		}
		hi := math.Min(float64(k)*StepM, maxRangeM)
		_, cHi, err := s.clearance(ctx, r, hi)
		if err != nil {
			return Hit{}, err
		}
		if cHi <= 0 {
			return s.refine(ctx, r, lo, cLo, hi, cHi)
		}
		lo, cLo = hi, cHi
	}
	return Hit{}, nil
}

// refine narrows a bracketing step, the ray at or above the ground at lo and
// at or below it at hi, to RefineM by bisection, then places the crossing by
// linear interpolation of the clearance within the final bracket. (The ray is
// level with the ground at lo only for an origin standing on it.)
func (s *Service) refine(ctx context.Context, r ray, lo, cLo, hi, cHi float64) (Hit, error) {
	for hi-lo > RefineM {
		if err := ctx.Err(); err != nil {
			return Hit{}, err
		}
		mid := (lo + hi) / 2
		_, c, err := s.clearance(ctx, r, mid)
		if err != nil {
			return Hit{}, err
		}
		if c > 0 {
			lo, cLo = mid, c
		} else {
			hi, cHi = mid, c
		}
	}
	rangeM := hi
	if cLo > cHi { // cLo >= 0 >= cHi
		rangeM = lo + (hi-lo)*cLo/(cLo-cHi)
	}
	p, c, err := s.clearance(ctx, r, rangeM)
	if err != nil {
		return Hit{}, err
	}
	p.Alt -= c // onto the ground
	return Hit{At: p, RangeM: rangeM, Found: true}, nil
}

// Visible reports whether the straight line from one point to another clears
// the ground: false when the ground cuts it more than 1 m short of to.
func (s *Service) Visible(ctx context.Context, from, to geo.LLA) (bool, error) {
	az, el, rangeM := geo.LLAToAER(from, to)
	hit, err := s.Intersect(ctx, from, az, el, rangeM)
	if err != nil {
		return false, err
	}
	return !hit.Found || hit.RangeM >= rangeM-1, nil
}

// DirectionCovariance is the position covariance of a ground point found by
// Intersect along a direction measured with standard deviations sigmaAzDeg
// and sigmaElDeg, in m² as the upper triangle (nn, ne, nu, ee, eu, uu) in the
// east-north-up frame at the hit, matching geo.PolarCovarianceNEU.
//
// The angular part re-intersects the rays at azimuth ± σaz and at elevation
// ± σel; the half-differences of each pair's hit points, horizontally, are
// two offset vectors whose outer products are summed. A perturbed ray that
// misses the ground counts as its point at maxRangeM. Looking steeply down
// the azimuth pair barely moves the hit, though pointing error there is the
// same in every direction, so the variance across the line of sight is held
// to at least (R·σel·|sin el|)², R the slant range to the hit: R·σel straight
// down, fading toward level where the azimuth pair dominates.
//
// The terrain's own vertical error adds a variance of
// (VerticalSigM / tan(depression))² along the line of sight, the depression
// being the ray's angle below the horizontal at the hit and its tangent held
// to at least VerticalSigM / maxRangeM, so the term never exceeds maxRangeM².
// uu is that terrain term alone, VerticalSigM²; the horizontal and vertical
// are uncorrelated (nu = eu = 0). A hit that was not Found is taken as the
// point at maxRangeM.
//
// ok is false when a term is not finite or a perturbed ray cannot be
// resolved: its Intersect fails, for instance because ctx is done or the
// terrain is unavailable.
func (s *Service) DirectionCovariance(ctx context.Context, origin geo.LLA, azDeg, elDeg, maxRangeM, sigmaAzDeg, sigmaElDeg float64, hit Hit) (cov [6]float64, ok bool) {
	centre := hit.At
	if !hit.Found {
		centre = geo.AERToLLA(origin, azDeg, elDeg, maxRangeM)
	}
	unresolved := false
	point := func(az, el float64) geo.ENU {
		if unresolved {
			return geo.ENU{}
		}
		h, err := s.Intersect(ctx, origin, az, el, maxRangeM)
		if err != nil {
			unresolved = true
			return geo.ENU{}
		}
		p := h.At
		if !h.Found {
			p = geo.AERToLLA(origin, az, el, maxRangeM)
		}
		return geo.ENUOf(centre, geo.ToECEF(p))
	}
	half := func(a, b geo.ENU) (e, n float64) { return (a.E - b.E) / 2, (a.N - b.N) / 2 }
	aE, aN := half(point(azDeg+sigmaAzDeg, elDeg), point(azDeg-sigmaAzDeg, elDeg))
	bE, bN := half(point(azDeg, elDeg+sigmaElDeg), point(azDeg, elDeg-sigmaElDeg))
	if unresolved {
		return cov, false
	}

	// The line of sight at the hit: back toward the origin, its horizontal
	// direction and the angle of depression. Looking straight down (or from
	// an origin at the hit) the direction falls back to the azimuth.
	back := geo.ENUOf(centre, geo.ToECEF(origin))
	horiz := math.Hypot(back.E, back.N)
	uE, uN := math.Sincos(azDeg * math.Pi / 180)
	var tanDep float64
	switch {
	case horiz > 1e-6:
		uE, uN = back.E/horiz, back.N/horiz
		tanDep = back.U / horiz
	case back.U > 0:
		tanDep = math.Inf(1)
	}
	tanDep = math.Max(tanDep, VerticalSigM/maxRangeM)
	sigAlong := VerticalSigM / tanDep
	varAlong := sigAlong * sigAlong

	nn := aN*aN + bN*bN + varAlong*uN*uN
	ne := aN*aE + bN*bE + varAlong*uN*uE
	ee := aE*aE + bE*bE + varAlong*uE*uE

	// Floor the variance across the line of sight (unit vector v, square
	// to u) where the azimuth pair leaves it short.
	slant := maxRangeM
	if hit.Found {
		slant = hit.RangeM
	}
	floor := slant * math.Abs(sigmaElDeg) * math.Pi / 180 * math.Abs(math.Sin(elDeg*math.Pi/180))
	vE, vN := -uN, uE
	if short := floor*floor - (nn*vN*vN + 2*ne*vN*vE + ee*vE*vE); short > 0 {
		nn += short * vN * vN
		ne += short * vN * vE
		ee += short * vE * vE
	}

	cov = [6]float64{nn, ne, 0, ee, 0, VerticalSigM * VerticalSigM} // nu = eu = 0
	return cov, finite(cov[:]...)
}
