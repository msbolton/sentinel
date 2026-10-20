import ms from 'milsymbol';
import { Affiliation, Entity, EntityType, TrackEnvironment } from '../../shared/models/entity.model';
import { Feed } from '../../shared/models/feed.model';
import { svgToDataUrl } from './cesium-config';

const TWENTY_DIGITS = /^\d{20}$/;
const symbolCache = new Map<string, string>();

/**
 * True when the connection that produced the entity's latest fix (or its
 * originating feed) is marked `config.military === true`.
 */
export function isFromMilitaryFeed(
  entity: Pick<Entity, 'feedId' | 'lastFeedId'>,
  feeds: readonly Feed[],
): boolean {
  const feedId = entity.lastFeedId || entity.feedId;
  if (!feedId) return false;
  const feed = feeds.find((f) => f.id === feedId);
  return feed?.config?.['military'] === true;
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

function setAndCodeFor(entity: Pick<Entity, 'entityType' | 'trackEnvironment' | 'platformData'>): [string, string] {
  if (entity.entityType === EntityType.SIGNAL) {
    return [sigintSymbolSet(entity.trackEnvironment), sigintEntityCode(entity.platformData?.rf?.emitterType)];
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
      return ['40', '110000'];
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
 */
export function sidcFor(
  entity: Pick<Entity, 'entityType' | 'affiliation' | 'trackEnvironment' | 'milStd2525dSymbol' | 'platformData'>,
): string {
  const explicit = entity.milStd2525dSymbol?.trim();
  if (explicit && TWENTY_DIGITS.test(explicit)) {
    return explicit;
  }
  const si = standardIdentityDigit(entity.affiliation);
  const [symbolSet, entityCode] = setAndCodeFor(entity);
  return `100${si}${symbolSet}0000${entityCode}0000`;
}

/**
 * Renders a MIL-STD-2525D SIDC with `milsymbol` at 32 px as a cached SVG data URL.
 */
export function symbolDataUrl(sidc: string): string {
  let cached = symbolCache.get(sidc);
  if (!cached) {
    const svg = new ms.Symbol(sidc, { size: 32 }).asSVG();
    cached = svgToDataUrl(svg);
    symbolCache.set(sidc, cached);
  }
  return cached;
}
