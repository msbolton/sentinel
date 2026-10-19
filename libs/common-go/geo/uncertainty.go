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
	l1, l2 := mean+d, math.Max(mean-d, 0)
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

// CEP is the radius of the circle holding half the horizontal error, from
// the standard deviations along the covariance's principal axes. 0.5887·(σ1+σ2)
// is exact for a circular error and within a few percent while σ2/σ1 ≥ 0.3.
func CEP(cNN, cNE, cEE float64) float64 {
	maj, min, _ := EllipseFromCov2(cNN, cNE, cEE)
	return 0.5887 * (maj + min) / Ellipse95
}
