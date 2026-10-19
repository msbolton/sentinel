import { emplacementDrawingOf, placementOf } from './emplacement-layer';

const fixed = { id: 'f', mobility: 'FIXED' as const, position: { lat: 50, lon: -4 }, boresightDeg: 270, azimuthReference: 'TRUE_NORTH' as const };
const ship = { id: 's', mobility: 'MOBILE' as const, position: { lat: 1, lon: 1 }, boresightDeg: 90, azimuthReference: 'PLATFORM' as const };

describe('placementOf', () => {
  it('draws a fixed emplacement where it was surveyed, whatever the stream says', () => {
    expect(placementOf(fixed, () => ({ lon: 9, lat: 9, heading: 0 }))).toEqual({ lon: -4, lat: 50, boresightDeg: 270 });
  });

  it('draws a mobile emplacement at its live fix, turning a platform-relative boresight with the heading', () => {
    expect(placementOf(ship, (id) => (id === 's' ? { lon: 3, lat: 4, heading: 300 } : null))).toEqual({ lon: 3, lat: 4, boresightDeg: 30 });
  });

  it('keeps the turned boresight within 0-360 when the live heading is negative', () => {
    expect(placementOf(ship, () => ({ lon: 3, lat: 4, heading: -100 }))).toEqual({ lon: 3, lat: 4, boresightDeg: 350 });
  });

  it('falls back to the stored position while the mobile one has no live fix', () => {
    expect(placementOf(ship, () => null)).toEqual({ lon: 1, lat: 1, boresightDeg: 90 });
  });

  it('has nowhere to draw an emplacement with no position at all', () => {
    expect(placementOf({ ...ship, position: null }, () => null)).toBeNull();
  });
});

describe('emplacementDrawingOf', () => {
  const loaded = new Set(['e-1']);
  const sensor = (value?: string) => ({ identities: value ? [{ type: 'EMPLACEMENT', value }] : [{ type: 'MMSI', value: '1' }] });

  it('hands an entity to the emplacement layer when it stands for a loaded emplacement', () => {
    expect(emplacementDrawingOf(sensor('e-1'), loaded)).toBe('e-1');
  });

  it('draws an entity claiming an emplacement nobody loaded as an ordinary entity', () => {
    expect(emplacementDrawingOf(sensor('forged'), loaded)).toBeNull();
  });

  it('draws an entity with no emplacement identity as an ordinary entity', () => {
    expect(emplacementDrawingOf(sensor(), loaded)).toBeNull();
  });
});
