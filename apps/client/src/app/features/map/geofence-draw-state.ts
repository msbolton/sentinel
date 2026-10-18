import { GeofenceConfig, GeofenceDraft, GeofenceShape, LngLat } from '../../shared/models/geofence.model';
import {
  circlePolygon,
  crossesAntimeridian,
  distinctPoints,
  formatDistance,
  geodesicDistanceM,
  rectanglePolygon,
} from './geofence-geometry';

/**
 * Everything the draw tool knows while a zone is being drawn. Pure data:
 * every function here returns a new session and never mutates.
 */
export interface DrawSession {
  shape: GeofenceShape;
  color: string;
  /** Radius for circle and ring, in metres; the second click or the toolbar sets it. */
  radiusM: number;
  /** Whether the user has set the radius (a click or the toolbar), as opposed to the default. */
  radiusSet: boolean;
  points: LngLat[];
  cursor: LngLat | null;
  anchor: { entityId: string; entityName: string; position: LngLat } | null;
}

export type FinishCheck = { ok: true } | { ok: false; reason: string };

export const REASONS = {
  polygon: 'Add at least three points',
  rectangle: 'Click the opposite corner',
  circle: 'Click to set the radius',
  ring: 'Click a track to anchor the ring',
  antimeridian: 'Zones cannot cross the antimeridian in this version',
} as const;

export function newSession(shape: GeofenceShape, color: string, radiusM = 5000): DrawSession {
  return { shape, color, radiusM, radiusSet: false, points: [], cursor: null, anchor: null };
}

/** Adds a point; for a circle the second point sets the radius instead. */
export function addPoint(s: DrawSession, p: LngLat): DrawSession {
  if (s.shape === 'circle' && s.points.length >= 1) {
    return { ...s, radiusM: geodesicDistanceM(s.points[0], p), radiusSet: true, points: [s.points[0], p] };
  }
  if (s.shape === 'rectangle' && s.points.length >= 2) {
    return { ...s, points: [s.points[0], p] };
  }
  return { ...s, points: [...s.points, p] };
}

export function setCursor(s: DrawSession, p: LngLat | null): DrawSession {
  return { ...s, cursor: p };
}

export function setAnchor(s: DrawSession, anchor: DrawSession['anchor']): DrawSession {
  return { ...s, anchor };
}

export function setRadius(s: DrawSession, radiusM: number): DrawSession {
  return { ...s, radiusM, radiusSet: true };
}

/** The ring to draw right now, cursor included where it stands in for the next click. */
export function previewRing(s: DrawSession): LngLat[] {
  switch (s.shape) {
    case 'polygon':
      return s.cursor && s.points.length > 0 ? [...s.points, s.cursor] : s.points;
    case 'rectangle': {
      const second = s.points[1] ?? s.cursor;
      return s.points[0] && second ? rectanglePolygon(s.points[0], second) : s.points;
    }
    case 'circle': {
      if (!s.points[0]) return [];
      const r = s.points[1] ? s.radiusM : s.cursor ? geodesicDistanceM(s.points[0], s.cursor) : s.radiusM;
      return r > 0 ? circlePolygon(s.points[0], r) : [];
    }
    case 'ring':
      return s.anchor && s.radiusM > 0 ? circlePolygon(s.anchor.position, s.radiusM) : [];
  }
}

/** What the label beside the cursor says: the edge being drawn, the radius, or the rectangle's size. */
export function liveMeasure(s: DrawSession): string {
  switch (s.shape) {
    case 'polygon': {
      const last = s.points[s.points.length - 1];
      return last && s.cursor ? formatDistance(geodesicDistanceM(last, s.cursor)) : '';
    }
    case 'rectangle': {
      const a = s.points[0];
      const b = s.points[1] ?? s.cursor;
      if (!a || !b) return '';
      const w = geodesicDistanceM([a[0], a[1]], [b[0], a[1]]);
      const h = geodesicDistanceM([a[0], a[1]], [a[0], b[1]]);
      return `${formatDistance(w)} × ${formatDistance(h)}`;
    }
    case 'circle': {
      if (!s.points[0]) return '';
      const r = s.points[1] ? s.radiusM : s.cursor ? geodesicDistanceM(s.points[0], s.cursor) : s.radiusM;
      return r > 0 ? formatDistance(r) : '';
    }
    case 'ring':
      return s.anchor ? formatDistance(s.radiusM) : '';
  }
}

export function canFinish(s: DrawSession): FinishCheck {
  switch (s.shape) {
    case 'polygon':
      if (distinctPoints(s.points) < 3) return { ok: false, reason: REASONS.polygon };
      break;
    case 'rectangle':
      if (distinctPoints(s.points) < 2) return { ok: false, reason: REASONS.rectangle };
      break;
    case 'circle':
      if (s.points.length < 1 || !s.radiusSet || !(s.radiusM > 0)) return { ok: false, reason: REASONS.circle };
      break;
    case 'ring':
      if (!s.anchor || !(s.radiusM > 0)) return { ok: false, reason: REASONS.ring };
      break;
  }
  const ring = s.shape === 'ring' ? [] : finalRing(s);
  if (crossesAntimeridian(ring)) return { ok: false, reason: REASONS.antimeridian };
  return { ok: true };
}

/** The shape part of a rule config for the finished session. */
export function finishShape(s: DrawSession): Pick<GeofenceConfig, 'shape' | 'polygon' | 'circle' | 'ring' | 'color'> {
  switch (s.shape) {
    case 'polygon':
      return { shape: 'polygon', polygon: [...s.points], color: s.color };
    case 'rectangle':
      return { shape: 'rectangle', polygon: rectanglePolygon(s.points[0], s.points[1]), color: s.color };
    case 'circle':
      return { shape: 'circle', polygon: circlePolygon(s.points[0], s.radiusM), circle: { center: s.points[0], radiusM: s.radiusM }, color: s.color };
    case 'ring':
      return {
        shape: 'ring',
        ring: { entityId: s.anchor!.entityId, entityName: s.anchor!.entityName, radiusM: s.radiusM },
        color: s.color,
      };
  }
}

function finalRing(s: DrawSession): LngLat[] {
  switch (s.shape) {
    case 'polygon': return s.points;
    case 'rectangle': return s.points.length >= 2 ? rectanglePolygon(s.points[0], s.points[1]) : s.points;
    case 'circle': return s.points[0] && s.radiusM > 0 ? circlePolygon(s.points[0], s.radiusM) : [];
    default: return [];
  }
}

/** The unsaved rule a finished session becomes; the panel's editor fills in the rest. */
export function draftFromSession(s: DrawSession, name = 'New geofence'): GeofenceDraft {
  return {
    name,
    ruleType: 'GEOFENCE',
    config: { ...finishShape(s), triggerOn: 'ENTRY' },
    monitoredEntityTypes: [],
    severity: 'HIGH',
    enabled: true,
  };
}
