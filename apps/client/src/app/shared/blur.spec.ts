import { BLUR_CELL_DEG, blurPosition } from './blur';

// The same vectors as the gateway's Blur (apps/api-gateway/internal/demo/blur_test.go):
// the browser blurs before it sends, and the gateway's blur of that is a no-op.
describe('blurPosition', () => {
  const near = (a: number, b: number) => expect(Math.abs(a - b)).toBeLessThan(1e-9);

  it('snaps to the centre of its 0.1° cell', () => {
    expect(BLUR_CELL_DEG).toBe(0.1);
    const p = blurPosition(51.5074, -0.1278);
    near(p.lat, 51.55);
    near(p.lon, -0.15);
  });

  it('handles the southern and eastern hemispheres', () => {
    const p = blurPosition(-33.8688, 151.2093);
    near(p.lat, -33.85);
    near(p.lon, 151.25);
  });

  it('maps every point of a cell to the same output', () => {
    expect(blurPosition(51.501, -0.199)).toEqual(blurPosition(51.599, -0.101));
  });

  it('clamps latitude 90 to the northernmost cell centre and -90 to the southernmost', () => {
    near(blurPosition(90, 0).lat, 89.95);
    near(blurPosition(-90, 0).lat, -89.95);
  });

  it('wraps longitude 180 to -179.95 and keeps -180 in range', () => {
    near(blurPosition(0, 180).lon, -179.95);
    near(blurPosition(0, -180).lon, -179.95);
  });

  it('is idempotent, so the gateway blurring a blurred point changes nothing', () => {
    for (const [lat, lon] of [[51.5074, -0.1278], [-33.8688, 151.2093], [90, 180], [0, 0], [48.8566, 2.3522]]) {
      const once = blurPosition(lat, lon);
      const twice = blurPosition(once.lat, once.lon);
      near(twice.lat, once.lat);
      near(twice.lon, once.lon);
    }
  });
});
