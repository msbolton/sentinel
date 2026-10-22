import { Entity } from '../../shared/models/entity.model';
import { DEAD_RECKONED_TYPES } from './cesium-config';

/**
 * Dead reckoning for moving tracks.
 *
 * Fixes arrive every poll (about 10 s for OpenSky). Between fixes each
 * tracked entity is advanced from its last fix along its velocity, so the
 * marker glides instead of jumping. A new fix is blended in over `blendMs`
 * by carrying the offset between the old and new predictions and shrinking
 * it to zero, unless the jump is large enough that snapping looks better.
 *
 * Pure functions plus a small tracker; no Cesium dependency so it is unit
 * testable.
 */

/** Last known state of one entity, in degrees, metres, m/s and epoch ms. */
export interface Fix {
  lat: number;
  lon: number;
  alt: number;
  vNorth: number;
  vEast: number;
  vUp: number;
  timeMs: number;
}

export interface Prediction {
  lat: number;
  lon: number;
  alt: number;
}

export interface DRState {
  fix: Fix;
  /** Display offset (degrees / metres) carried from the previous fix. */
  offsetLat: number;
  offsetLon: number;
  offsetAlt: number;
  blendStartMs: number;
}

export interface DRDisplay extends Prediction {
  /** True while the position is extrapolated beyond the last fix. */
  extrapolating: boolean;
}

export interface DROptions {
  /** Stop extrapolating this long after the last fix. */
  maxExtrapolationMs: number;
  /** Blend a new fix in over this long. */
  blendMs: number;
  /** Snap instead of blending when the correction is larger than this. */
  snapDistanceM: number;
  /** Entities slower than this are not tracked (they would not move anyway). */
  minSpeedMps: number;
  /** Upper bound on tracked entities, to cap per-frame work. */
  maxTracked: number;
}

export const DR_DEFAULTS: Readonly<DROptions> = Object.freeze({
  maxExtrapolationMs: 60_000,
  blendMs: 1_000,
  snapDistanceM: 5_000,
  minSpeedMps: 1,
  maxTracked: 3_000,
});

const KNOTS_TO_MPS = 0.514444;
const METERS_PER_DEG_LAT = 111_320;
/** cos(89°): keeps the longitude scale finite near the poles. */
const MIN_COS_LAT = Math.cos((89 * Math.PI) / 180);

function metersPerDegLon(latDeg: number): number {
  return METERS_PER_DEG_LAT * Math.max(MIN_COS_LAT, Math.cos((latDeg * Math.PI) / 180));
}

export function wrapLongitude(lon: number): number {
  if (lon >= -180 && lon <= 180) {
    return lon; // avoid floating-point drift on the common path
  }
  let x = ((((lon + 180) % 360) + 360) % 360) - 180;
  if (x === -180 && lon > 0) x = 180;
  return x;
}

/**
 * Build a fix from an entity, or null when the entity cannot be
 * dead-reckoned: not one of DEAD_RECKONED_TYPES (aircraft, drones and
 * vessels), no position, no usable velocity, or effectively stationary.
 */
export function fixFromEntity(entity: Entity, nowMs: number, opts: DROptions = DR_DEFAULTS): Fix | null {
  if (!DEAD_RECKONED_TYPES.has(entity.entityType) || !entity.position) {
    return null;
  }
  const velocity = entity.kinematics?.velocity;
  let vNorth: number | undefined;
  let vEast: number | undefined;
  if (velocity && Number.isFinite(velocity.north) && Number.isFinite(velocity.east)) {
    vNorth = velocity.north;
    vEast = velocity.east;
  } else if (entity.speedKnots != null && Number.isFinite(entity.speedKnots)) {
    const track = entity.course ?? entity.heading;
    if (track == null || !Number.isFinite(track)) {
      return null;
    }
    const speed = entity.speedKnots * KNOTS_TO_MPS;
    const rad = (track * Math.PI) / 180;
    vNorth = speed * Math.cos(rad);
    vEast = speed * Math.sin(rad);
  }
  if (vNorth === undefined || vEast === undefined) {
    return null;
  }
  if (Math.hypot(vNorth, vEast) < opts.minSpeedMps) {
    return null;
  }

  const vUp = velocity && Number.isFinite(velocity.up) ? velocity.up : 0;
  const stamp = Date.parse(entity.lastSeenAt ?? entity.updatedAt ?? '');
  const timeMs = Number.isFinite(stamp) ? Math.min(stamp, nowMs) : nowMs;

  return {
    lat: entity.position.latitude,
    lon: entity.position.longitude,
    alt: entity.position.altitude ?? 0,
    vNorth,
    vEast,
    vUp,
    timeMs,
  };
}

/**
 * Position of a fix advanced to `nowMs`, or null once the fix is older than
 * `maxMs`. Always predicts from the fix with the full elapsed time, so the
 * error never accumulates beyond one fix interval.
 */
export function predict(fix: Fix, nowMs: number, maxMs: number = DR_DEFAULTS.maxExtrapolationMs): Prediction | null {
  const elapsedMs = nowMs - fix.timeMs;
  if (elapsedMs > maxMs) {
    return null;
  }
  const dt = Math.max(0, elapsedMs) / 1000;
  const lat = fix.lat + (fix.vNorth * dt) / METERS_PER_DEG_LAT;
  const lon = wrapLongitude(fix.lon + (fix.vEast * dt) / metersPerDegLon(fix.lat));
  const alt = Math.max(0, fix.alt + fix.vUp * dt);
  return { lat, lon, alt };
}

/**
 * Fold a new fix into the state. The offset between where the old state
 * was being drawn and where the new fix predicts for `nowMs` is kept and
 * blended away, so the marker does not jump; a correction larger than
 * `snapDistanceM` snaps instead.
 */
export function applyFix(prev: DRState | undefined, fix: Fix, nowMs: number, opts: DROptions = DR_DEFAULTS): DRState {
  const next: DRState = { fix, offsetLat: 0, offsetLon: 0, offsetAlt: 0, blendStartMs: nowMs };
  if (!prev) {
    return next;
  }
  const shown = displayPosition(prev, nowMs, opts);
  const target = predict(fix, nowMs, opts.maxExtrapolationMs);
  if (!shown || !target) {
    return next;
  }
  const offsetLat = shown.lat - target.lat;
  let offsetLon = shown.lon - target.lon;
  if (offsetLon > 180) offsetLon -= 360;
  if (offsetLon < -180) offsetLon += 360;
  const offsetAlt = shown.alt - target.alt;

  const distanceM = Math.hypot(offsetLat * METERS_PER_DEG_LAT, offsetLon * metersPerDegLon(target.lat), offsetAlt);
  if (distanceM > opts.snapDistanceM) {
    return next;
  }
  next.offsetLat = offsetLat;
  next.offsetLon = offsetLon;
  next.offsetAlt = offsetAlt;
  return next;
}

/** Where to draw the entity now, or null when its fix has expired. */
export function displayPosition(state: DRState, nowMs: number, opts: DROptions = DR_DEFAULTS): DRDisplay | null {
  const p = predict(state.fix, nowMs, opts.maxExtrapolationMs);
  if (!p) {
    return null;
  }
  const blend = opts.blendMs > 0 ? 1 - Math.min(1, Math.max(0, (nowMs - state.blendStartMs) / opts.blendMs)) : 0;
  return {
    lat: p.lat + state.offsetLat * blend,
    lon: wrapLongitude(p.lon + state.offsetLon * blend),
    alt: Math.max(0, p.alt + state.offsetAlt * blend),
    extrapolating: nowMs > state.fix.timeMs,
  };
}

/** Tracks the dead-reckoned state of every moving entity on the map. */
export class DeadReckoningTracker {
  private readonly states = new Map<string, DRState>();

  constructor(private readonly opts: DROptions = DR_DEFAULTS) {}

  get size(): number {
    return this.states.size;
  }

  has(id: string): boolean {
    return this.states.has(id);
  }

  /**
   * Record a fix for an entity. Returns true when the entity is now
   * dead-reckoned; false when it is not eligible (and no longer tracked).
   */
  upsert(entity: Entity, nowMs: number): boolean {
    const fix = fixFromEntity(entity, nowMs, this.opts);
    if (!fix) {
      this.states.delete(entity.id);
      return false;
    }
    const prev = this.states.get(entity.id);
    if (!prev && this.states.size >= this.opts.maxTracked) {
      return false;
    }
    this.states.set(entity.id, applyFix(prev, fix, nowMs, this.opts));
    return true;
  }

  remove(id: string): void {
    this.states.delete(id);
  }

  clear(): void {
    this.states.clear();
  }

  /** Current display position of one entity, or null when not tracked. */
  positionOf(id: string, nowMs: number): DRDisplay | null {
    const state = this.states.get(id);
    return state ? displayPosition(state, nowMs, this.opts) : null;
  }

  /**
   * Advance every tracked entity to `nowMs`. Entities whose fix has expired
   * are dropped and not returned.
   */
  step(nowMs: number): Array<[string, DRDisplay]> {
    const out: Array<[string, DRDisplay]> = [];
    for (const [id, state] of this.states) {
      const p = displayPosition(state, nowMs, this.opts);
      if (!p) {
        this.states.delete(id);
        continue;
      }
      out.push([id, p]);
    }
    return out;
  }
}
