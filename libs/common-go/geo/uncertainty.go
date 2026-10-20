package geo

import "math"

// Ellipse95 scales a standard deviation to the half-axis of the 95%
// confidence ellipse of a 2-D normal error: sqrt of the χ² quantile with two
// degrees of freedom at 0.95 (5.991).
const Ellipse95 = 2.4477

// Sigma is one standard deviation per polar axis; a nil axis contributes
// nothing.
type Sigma struct{ RangeM, AzimuthDeg, ElevationDeg *float64 }

// PolarCovarianceNEU propagates polar sigmas through the Jacobian of the
// polar-to-ENU conversion into the covariance of the converted position, in
// m², as the upper triangle [nn, ne, nu, ee, eu, uu] (north, east, up: the
// order of obs.PosCovariance). ok is false when no sigma is given.
func PolarCovarianceNEU(azDeg, elDeg, rangeM float64, s Sigma) (cov [6]float64, ok bool) {
	if s.RangeM == nil && s.AzimuthDeg == nil && s.ElevationDeg == nil {
		return cov, false
	}
	sq := func(v *float64, scale float64) float64 {
		if v == nil {
			return 0
		}
		x := *v * scale
		return x * x
	}
	varR, varAz, varEl := sq(s.RangeM, 1), sq(s.AzimuthDeg, math.Pi/180), sq(s.ElevationDeg, math.Pi/180)
	sAz, cAz := math.Sincos(rad(azDeg))
	sEl, cEl := math.Sincos(rad(elDeg))
	// Columns of d(e, n, u)/d(range, azimuth, elevation), angles in radians.
	const e, n, u = 0, 1, 2
	jr := [3]float64{cEl * sAz, cEl * cAz, sEl}
	ja := [3]float64{rangeM * cEl * cAz, -rangeM * cEl * sAz, 0}
	je := [3]float64{-rangeM * sEl * sAz, -rangeM * sEl * cAz, rangeM * cEl}
	c := func(i, j int) float64 { return jr[i]*jr[j]*varR + ja[i]*ja[j]*varAz + je[i]*je[j]*varEl }
	return [6]float64{c(n, n), c(n, e), c(n, u), c(e, e), c(e, u), c(u, u)}, true
}

// EllipseFromCov2 is the 95% ellipse of a horizontal covariance: its
// half-axes in metres and the major axis's orientation in degrees from north
// toward east, in [0, 180).
func EllipseFromCov2(cNN, cNE, cEE float64) (semiMajorM, semiMinorM, orientationDeg float64) {
	mean := (cNN + cEE) / 2
	d := math.Hypot((cNN-cEE)/2, cNE)
	l1, l2 := math.Max(mean+d, 0), math.Max(mean-d, 0)
	o := deg(0.5 * math.Atan2(2*cNE, cNN-cEE))
	return Ellipse95 * math.Sqrt(l1), Ellipse95 * math.Sqrt(l2), math.Mod(o+180, 180)
}

// Cov2FromEllipse is the horizontal covariance of a 95% ellipse given by its
// half-axes and the major axis's orientation from north.
func Cov2FromEllipse(semiMajorM, semiMinorM, orientationDeg float64) (cNN, cNE, cEE float64) {
	s1, s2 := semiMajorM/Ellipse95, semiMinorM/Ellipse95
	v1, v2 := s1*s1, s2*s2
	sO, cO := math.Sincos(rad(orientationDeg))
	return v1*cO*cO + v2*sO*sO, (v1 - v2) * sO * cO, v1*sO*sO + v2*cO*cO
}

// norm95 is the two-sided 95% quantile of a 1-D standard normal (P(|Z| <= norm95) = 0.95),
// the limit of CEP95 / sigma1 as sigma2 / sigma1 -> 0.
const norm95 = 1.959963984540054

// CEP95 is the radius (in metres) of the circle centred on the mean that
// contains 95% of the horizontal normal error with covariance (cNN, cNE, cEE).
// From the principal standard deviations σ1 = maj/Ellipse95 and
// σ2 = min/Ellipse95 (k = σ2/σ1 ∈ [0, 1]), P(X²+Y² ≤ (u·σ1)²) equals
// 1 - (2/π)·∫₀^(π/2) exp(-u² / (2·(cos²ψ + k²·sin²ψ))) dψ; CEP95 solves
// for u ∈ [norm95, Ellipse95] by bisection. For a circle (k = 1) this returns
// Ellipse95·σ = 2.4477·σ; as k → 0 it approaches 1.9600·σ1.
func CEP95(cNN, cNE, cEE float64) float64 {
	maj, min, _ := EllipseFromCov2(cNN, cNE, cEE)
	if maj <= 0 {
		return 0
	}
	s1, s2 := maj/Ellipse95, min/Ellipse95
	k := s2 / s1
	if k >= 1-1e-12 {
		return maj
	}
	if k <= 1e-12 {
		return norm95 * s1
	}
	k2 := k * k
	const steps = 64
	const dPsi = (math.Pi / 2) / steps
	tailProb := func(u float64) float64 {
		u2Half := 0.5 * u * u
		sum := 0.0
		for i := 0; i < steps; i++ {
			psi := (float64(i) + 0.5) * dPsi
			s, c := math.Sincos(psi)
			denom := c*c + k2*s*s
			sum += math.Exp(-u2Half / denom)
		}
		return (2 / math.Pi) * sum * dPsi
	}
	lo, hi := norm95, Ellipse95
	for i := 0; i < 40; i++ {
		mid := 0.5 * (lo + hi)
		if tailProb(mid) > 0.05 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return 0.5 * (lo + hi) * s1
}

// CEP returns the 95th-percentile circular error radius (alias for CEP95).
func CEP(cNN, cNE, cEE float64) float64 {
	return CEP95(cNN, cNE, cEE)
}
