import { PositionCovariance } from '../../shared/models/platform-data.model';

/**
 * 95% confidence scale factor for a 2D Gaussian error ellipse:
 * sqrt(-2 * ln(1 - 0.95)) = sqrt(2 * ln(20)) ≈ 2.447746830680816.
 */
export const ELLIPSE_95 = Math.sqrt(-2 * Math.log(1 - 0.95));

export interface EllipseAxes {
  /** 95% semi-major axis in metres. */
  semiMajorM: number;
  /** 95% semi-minor axis in metres. */
  semiMinorM: number;
  /** Major-axis bearing in degrees clockwise from True North, in [0, 180). */
  orientationDeg: number;
  /** Major-axis rotation in radians counter-clockwise from East, for Cesium EllipseGraphics. */
  rotationRad: number;
}

/**
 * Recovers the 95% horizontal uncertainty ellipse from a 2x2 North-East
 * position covariance matrix (m²). Returns null when the covariance is absent,
 * non-finite, or not positive-definite.
 */
export function ellipseFromCovariance(cov?: PositionCovariance | null): EllipseAxes | null {
  if (!cov) return null;
  const nn = cov.posCovLatLat;
  const ne = cov.posCovLatLon ?? 0;
  const ee = cov.posCovLonLon;
  if (
    nn == null ||
    ee == null ||
    !Number.isFinite(nn) ||
    !Number.isFinite(ne) ||
    !Number.isFinite(ee) ||
    nn <= 0 ||
    ee <= 0
  ) {
    return null;
  }
  const det = nn * ee - ne * ne;
  if (!(det > 0)) return null;

  const trace = nn + ee;
  const diff = nn - ee;
  const disc = Math.hypot(diff, 2 * ne);
  const lambda1 = 0.5 * (trace + disc);
  const lambda2 = 0.5 * (trace - disc);
  if (!(lambda1 > 0) || !(lambda2 > 0)) return null;

  const semiMajorM = ELLIPSE_95 * Math.sqrt(lambda1);
  const semiMinorM = ELLIPSE_95 * Math.sqrt(lambda2);

  // Bearing clockwise from North in [0, pi)
  const thetaRad = disc === 0 ? 0 : (((0.5 * Math.atan2(2 * ne, diff)) % Math.PI) + Math.PI) % Math.PI;
  const orientationDeg = (thetaRad * 180) / Math.PI;
  // Cesium EllipseGraphics.rotation is counter-clockwise from East
  const rotationRad = Math.PI / 2 - thetaRad;

  return { semiMajorM, semiMinorM, orientationDeg, rotationRad };
}
