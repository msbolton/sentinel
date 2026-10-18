import { addPoint, canFinish, draftFromSession, finishShape, liveMeasure, newSession, previewRing, setAnchor, setCursor, setRadius } from './geofence-draw-state';
import { circlePolygon, geodesicDistanceM } from './geofence-geometry';
import { LngLat } from '../../shared/models/geofence.model';

const A: LngLat = [55.0, 25.0];
const B: LngLat = [55.1, 25.0];
const C: LngLat = [55.1, 25.1];

describe('geofence draw session', () => {
  it('refuses a polygon with fewer than three distinct points', () => {
    const s = addPoint(addPoint(newSession('polygon', '#f97316'), A), B);
    expect(canFinish(s)).toEqual({ ok: false, reason: 'Add at least three points' });
    expect(canFinish(addPoint(s, B))).toEqual({ ok: false, reason: 'Add at least three points' });
  });

  it('finishes a polygon into its points', () => {
    const s = addPoint(addPoint(addPoint(newSession('polygon', '#f97316'), A), B), C);
    expect(canFinish(s)).toEqual({ ok: true });
    expect(finishShape(s)).toEqual({ shape: 'polygon', polygon: [A, B, C], color: '#f97316' });
  });

  it('finishes a rectangle into four corners', () => {
    const one = addPoint(newSession('rectangle', '#3b82f6'), A);
    expect(canFinish(one)).toEqual({ ok: false, reason: 'Click the opposite corner' });
    const s = addPoint(one, C);
    expect(finishShape(s)).toEqual({ shape: 'rectangle', polygon: [[55, 25], [55, 25.1], [55.1, 25.1], [55.1, 25]], color: '#3b82f6' });
  });

  it('finishes a circle into a 64-point polygon and keeps the circle', () => {
    const origin = addPoint(newSession('circle', '#06b6d4'), A);
    expect(canFinish(origin)).toEqual({ ok: false, reason: 'Click to set the radius' });
    const edge: LngLat = [55.1489, 25.0]; // ≈ 15 km east
    const s = addPoint(origin, edge);
    const out = finishShape(s);
    expect(out.shape).toBe('circle');
    expect(out.polygon).toHaveLength(64);
    expect(Math.abs((out.circle?.radiusM ?? 0) - geodesicDistanceM(A, edge))).toBeLessThan(1);
    expect(out.circle?.center).toEqual(A);
    // a typed radius replaces the clicked one
    expect(finishShape(setRadius(s, 20_000)).circle?.radiusM).toBe(20_000);
  });

  it('previews a ring around its anchor and finishes with ring and no polygon', () => {
    const empty = newSession('ring', '#f59e0b', 8000);
    expect(canFinish(empty)).toEqual({ ok: false, reason: 'Click a track to anchor the ring' });
    const s = setAnchor(empty, { entityId: 'e9', entityName: 'MV HORIZON STAR', position: A });
    expect(previewRing(s)).toHaveLength(64);
    expect(canFinish(s)).toEqual({ ok: true });
    expect(finishShape(s)).toEqual({ shape: 'ring', ring: { entityId: 'e9', entityName: 'MV HORIZON STAR', radiusM: 8000 }, color: '#f59e0b' });
  });

  it('refuses a shape crossing the antimeridian', () => {
    const s = addPoint(addPoint(addPoint(newSession('polygon', '#f97316'), [179, 10]), [-179, 10]), [-179, 11]);
    expect(canFinish(s)).toEqual({ ok: false, reason: 'Zones cannot cross the antimeridian in this version' });
  });

  it('measures the trailing edge live', () => {
    const s = setCursor(addPoint(newSession('polygon', '#f97316'), A), [55.0378, 25.0]); // ≈ 3.8 km east
    expect(liveMeasure(s)).toBe('3.8 km');
    expect(previewRing(s)).toEqual([A, [55.0378, 25.0]]);
    expect(liveMeasure(setCursor(s, null))).toBe('');
  });

  it('turns a finished session into a draft with the defaults', () => {
    const s = addPoint(addPoint(addPoint(newSession('polygon', '#f97316'), A), B), C);
    expect(draftFromSession(s)).toEqual({
      name: 'New geofence',
      ruleType: 'GEOFENCE',
      config: { shape: 'polygon', polygon: [A, B, C], color: '#f97316', triggerOn: 'ENTRY' },
      monitoredEntityTypes: [],
      severity: 'HIGH',
      enabled: true,
    });
    expect(draftFromSession(s, 'Anchorage').name).toBe('Anchorage');
  });

  it("sets a ring's radius from the click after its anchor", () => {
    const anchored = setAnchor(newSession('ring', '#f59e0b'), { entityId: 'e9', entityName: 'MV HORIZON STAR', position: A });
    const edge: LngLat = [55.1489, 25.0]; // ≈ 15 km east
    const s = addPoint(anchored, edge);
    expect(s.radiusSet).toBe(true);
    expect(Math.abs(s.radiusM - geodesicDistanceM(A, edge))).toBeLessThan(1);
    expect(s.points).toEqual([]);
  });

  it('previews a ring at the cursor until its radius is set', () => {
    const anchored = setCursor(setAnchor(newSession('ring', '#f59e0b'), { entityId: 'e9', entityName: 'MV HORIZON STAR', position: A }), [55.0378, 25.0]);
    expect(liveMeasure(anchored)).toBe('3.8 km');
    expect(previewRing(anchored)).toEqual(circlePolygon(A, geodesicDistanceM(A, [55.0378, 25.0])));
    const typed = setRadius(anchored, 8000);
    expect(liveMeasure(typed)).toBe('8.0 km');
    expect(previewRing(typed)).toEqual(circlePolygon(A, 8000));
  });

  it('refuses shapes that enclose no area', () => {
    const flat = addPoint(addPoint(newSession('rectangle', '#3b82f6'), A), B); // corners on one latitude
    expect(canFinish(flat)).toEqual({ ok: false, reason: 'The shape must enclose an area' });
    const line = addPoint(addPoint(addPoint(newSession('polygon', '#f97316'), A), B), [55.2, 25.0]);
    expect(canFinish(line)).toEqual({ ok: false, reason: 'The shape must enclose an area' });
  });

  it('previews a circle at its typed radius once one is set', () => {
    const typed = setCursor(setRadius(addPoint(newSession('circle', '#06b6d4'), A), 20_000), [55.0378, 25.0]);
    expect(liveMeasure(typed)).toBe('20.0 km');
    expect(previewRing(typed)).toEqual(circlePolygon(A, 20_000));
  });
});
