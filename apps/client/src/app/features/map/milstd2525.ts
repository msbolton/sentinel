import ms from 'milsymbol';
import { Affiliation, Entity, EntityType, TrackEnvironment } from '../../shared/models/entity.model';
import { RFData } from '../../shared/models/platform-data.model';
import { Feed } from '../../shared/models/feed.model';
import { svgToDataUrl } from './cesium-config';

const TWENTY_DIGITS = /^\d{20}$/;
export const MAX_SYMBOL_CACHE_SIZE = 512;
const symbolCache = new Map<string, string>();

/** Visible for tests: number of cached symbol data URLs. */
export function symbolCacheSize(): number {
  return symbolCache.size;
}

/**
 * True when any feed in `feedId`, `lastFeedId` or `sources[].feedId` is a
 * loaded feed with `config.military === true`.
 */
export function isFromMilitaryFeed(
  entity: Pick<Entity, 'feedId' | 'lastFeedId' | 'sources'>,
  feeds: readonly Feed[],
): boolean {
  const candidates = new Set<string>();
  if (entity.feedId) candidates.add(entity.feedId);
  if (entity.lastFeedId) candidates.add(entity.lastFeedId);
  if (Array.isArray(entity.sources)) {
    for (const s of entity.sources) {
      if (s?.feedId) candidates.add(s.feedId);
    }
  }
  if (candidates.size === 0) return false;
  for (const f of feeds) {
    if (candidates.has(f.id) && f.config?.['military'] === true) {
      return true;
    }
  }
  return false;
}

function standardIdentityDigit(affiliation?: Affiliation | string): string {
  switch (affiliation) {
    case Affiliation.PENDING:
      return '0';
    case Affiliation.UNKNOWN:
      return '1';
    case Affiliation.ASSUMED_FRIENDLY:
      return '2';
    case Affiliation.FRIENDLY:
      return '3';
    case Affiliation.NEUTRAL:
      return '4';
    case Affiliation.SUSPECT:
      return '5';
    case Affiliation.HOSTILE:
      return '6';
    default:
      return '1';
  }
}

function sigintSymbolSet(env?: TrackEnvironment | string): string {
  switch (env) {
    case TrackEnvironment.SPACE:
      return '50';
    case TrackEnvironment.AIR:
      return '51';
    case TrackEnvironment.GROUND:
      return '52';
    case TrackEnvironment.SEA_SURFACE:
      return '53';
    case TrackEnvironment.SUBSURFACE:
      return '54';
    default:
      return '52';
  }
}

function sigintEntityCode(emitterType?: string): string {
  const t = (emitterType ?? '').trim().toUpperCase();
  if (t === 'SATCOM' || t === 'COMMS' || t === 'DATALINK' || t === 'COMMUNICATIONS' || t.startsWith('COMM')) {
    return '110100';
  }
  if (t === 'JAMMER' || t === 'ECM' || t.startsWith('JAM')) {
    return '110200';
  }
  if (t === 'RADAR' || t === 'RADAR_SEARCH' || t === 'RADAR_TRACK' || t.startsWith('RADAR')) {
    return '110300';
  }
  return '110000';
}

function setAndCodeFor(
  entity: Pick<Entity, 'entityType' | 'trackEnvironment' | 'platformData'>,
  rf?: RFData,
): [string, string] {
  if (entity.entityType === EntityType.SIGNAL) {
    const emitterType = rf?.emitterType ?? entity.platformData?.rf?.emitterType;
    return [sigintSymbolSet(entity.trackEnvironment), sigintEntityCode(emitterType)];
  }
  switch (entity.entityType) {
    case EntityType.AIRCRAFT:
      return ['01', '110000'];
    case EntityType.DRONE:
      return ['01', '110300'];
    case EntityType.SATELLITE:
      return ['05', '110000'];
    case EntityType.PERSON:
    case EntityType.UNIT:
      return ['10', '110000'];
    case EntityType.VEHICLE:
      return ['15', '120000'];
    case EntityType.EQUIPMENT:
    case EntityType.SENSOR:
      return ['15', '220000'];
    case EntityType.FACILITY:
      return ['20', '110000'];
    case EntityType.VESSEL:
      return entity.trackEnvironment === TrackEnvironment.SUBSURFACE ? ['35', '110000'] : ['30', '120000'];
    case EntityType.CYBER:
      return ['60', '110000'];
    default:
      switch (entity.trackEnvironment) {
        case TrackEnvironment.AIR:
          return ['01', '110000'];
        case TrackEnvironment.SPACE:
          return ['05', '110000'];
        case TrackEnvironment.SEA_SURFACE:
          return ['30', '120000'];
        case TrackEnvironment.SUBSURFACE:
          return ['35', '110000'];
        default:
          return ['10', '110000'];
      }
  }
}

/**
 * Builds a 20-digit MIL-STD-2525D SIDC for `entity`, using `entity.milStd2525dSymbol`
 * directly when it is already a 20-digit numeric code.
 * Context is exercise ('1') when `entity.exercise` is true, else reality ('0').
 */
export function sidcFor(
  entity: Pick<Entity, 'entityType' | 'affiliation' | 'trackEnvironment' | 'exercise' | 'milStd2525dSymbol' | 'platformData'>,
  rf?: RFData,
): string {
  const explicit = entity.milStd2525dSymbol?.trim();
  if (explicit && TWENTY_DIGITS.test(explicit)) {
    return explicit;
  }
  const ctx = entity.exercise ? '1' : '0';
  const si = standardIdentityDigit(entity.affiliation);
  const [symbolSet, entityCode] = setAndCodeFor(entity, rf);
  return `10${ctx}${si}${symbolSet}0000${entityCode}0000`;
}

/**
 * Renders a MIL-STD-2525D SIDC with `milsymbol` as a bounded LRU-cached SVG data URL.
 */
export function symbolImage(sidc: string, size = 32): string {
  const key = size === 32 ? sidc : `${sidc}@${size}`;
  const existing = symbolCache.get(key);
  if (existing !== undefined) {
    // Refresh LRU order
    symbolCache.delete(key);
    symbolCache.set(key, existing);
    return existing;
  }
  const svg = new ms.Symbol(sidc, { size }).asSVG();
  const cached = svgToDataUrl(svg);
  if (symbolCache.size >= MAX_SYMBOL_CACHE_SIZE) {
    const oldest = symbolCache.keys().next().value;
    if (oldest !== undefined) {
      symbolCache.delete(oldest);
    }
  }
  symbolCache.set(key, cached);
  return cached;
}

export const symbolDataUrl = symbolImage;
