import { ELLIPSE_95, ellipseFromCovariance } from './uncertainty';
import { PositionCovariance } from '../../shared/models/platform-data.model';

function covFromEllipse(semiMajorM: number, semiMinorM: number, orientationDeg: number): PositionCovariance {
  const s1 = semiMajorM / ELLIPSE_95;
  const s2 = semiMinorM / ELLIPSE_95;
  const s1Sq = s1 * s1;
  const s2Sq = s2 * s2;
  const theta = (orientationDeg * Math.PI) / 180;
  const c = Math.cos(theta);
  const s = Math.sin(theta);
  return {
    pnPn: s1Sq * c * c + s2Sq * s * s,
    pnPe: (s1Sq - s2Sq) * s * c,
    pnPu: 0,
    pePe: s1Sq * s * s + s2Sq * c * c,
    pePu: 0,
    puPu: 0,
  };
}

describe('ellipseFromCovariance', () => {
  it('round-trips a 95% error ellipse (1800 m x 420 m at 35° clockwise from North)', () => {
    const cov = covFromEllipse(1800, 420, 35);
    const res = ellipseFromCovariance(cov);
    expect(res).not.toBeNull();
    expect(res!.semiMajorM).toBeCloseTo(1800, 6);
    expect(res!.semiMinorM).toBeCloseTo(420, 6);
    expect(res!.orientationDeg).toBeCloseTo(35, 6);
    // 35° CW from North = 55° CCW from East
    expect(res!.rotationRad).toBeCloseTo((55 * Math.PI) / 180, 6);
  });

  it('decodes a gateway stream entity payload shaped like bridge.go output', () => {
    // bridge.go serialises en.Kinematics.PositionCovariance with keys pnPn/pnPe/pnPu/pePe/pePu/puPu
    const wireEntity = JSON.parse(
      JSON.stringify({
        id: 'sig-1',
        entityType: 'SIGNAL',
        kinematics: {
          positionCovariance: covFromEllipse(1800, 420, 32),
        },
      }),
    );
    const res = ellipseFromCovariance(wireEntity.kinematics?.positionCovariance);
    expect(res).not.toBeNull();
    expect(res!.semiMajorM).toBeCloseTo(1800, 6);
    expect(res!.semiMinorM).toBeCloseTo(420, 6);
    expect(res!.orientationDeg).toBeCloseTo(32, 6);
    expect(res!.rotationRad).toBeCloseTo(((90 - 32) * Math.PI) / 180, 6);
  });

  it('handles axis-aligned and circular covariances', () => {
    const northMajor = ellipseFromCovariance(covFromEllipse(1000, 300, 0))!;
    expect(northMajor.semiMajorM).toBeCloseTo(1000, 6);
    expect(northMajor.semiMinorM).toBeCloseTo(300, 6);
    expect(northMajor.orientationDeg).toBeCloseTo(0, 6);
    expect(northMajor.rotationRad).toBeCloseTo(Math.PI / 2, 6);

    const eastMajor = ellipseFromCovariance(covFromEllipse(1000, 300, 90))!;
    expect(eastMajor.semiMajorM).toBeCloseTo(1000, 6);
    expect(eastMajor.semiMinorM).toBeCloseTo(300, 6);
    expect(eastMajor.orientationDeg).toBeCloseTo(90, 6);
    expect(eastMajor.rotationRad).toBeCloseTo(0, 6);

    const circle = ellipseFromCovariance(covFromEllipse(500, 500, 0))!;
    expect(circle.semiMajorM).toBeCloseTo(500, 6);
    expect(circle.semiMinorM).toBeCloseTo(500, 6);
  });

  it('returns null for absent, zero, negative, or non-positive-definite covariances', () => {
    expect(ellipseFromCovariance(null)).toBeNull();
    expect(ellipseFromCovariance({})).toBeNull();
    expect(ellipseFromCovariance({ pnPn: 0, pePe: 100 })).toBeNull();
    expect(ellipseFromCovariance({ pnPn: -10, pePe: 100 })).toBeNull();
    expect(ellipseFromCovariance({ pnPn: 10, pnPe: 20, pePe: 10 })).toBeNull();
    expect(ellipseFromCovariance({ pnPn: Number.NaN, pePe: 10 })).toBeNull();
  });
});
