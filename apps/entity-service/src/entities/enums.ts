export {
  EntityType,
  EntitySource,
  Classification,
} from '@sentinel/proto-gen';
import { Classification } from '@sentinel/proto-gen';

// These enums live in @sentinel/shared-models (ESM) but entity-service is CJS,
// so we define them locally to avoid CJS/ESM interop issues.

export enum TrackEnvironment {
  AIR = 'AIR',
  SEA_SURFACE = 'SEA_SURFACE',
  SUBSURFACE = 'SUBSURFACE',
  GROUND = 'GROUND',
  SPACE = 'SPACE',
  UNKNOWN = 'UNKNOWN',
}

export enum TrackProcessingState {
  LIVE = 'LIVE',
  PREDICTED = 'PREDICTED',
  DEAD_RECKONED = 'DEAD_RECKONED',
  HYPOTHESIZED = 'HYPOTHESIZED',
  HISTORICAL = 'HISTORICAL',
}

export enum NavigationalStatus {
  UNDER_WAY_USING_ENGINE = 'UNDER_WAY_USING_ENGINE',
  AT_ANCHOR = 'AT_ANCHOR',
  NOT_UNDER_COMMAND = 'NOT_UNDER_COMMAND',
  RESTRICTED_MANOEUVRABILITY = 'RESTRICTED_MANOEUVRABILITY',
  CONSTRAINED_BY_DRAUGHT = 'CONSTRAINED_BY_DRAUGHT',
  MOORED = 'MOORED',
  AGROUND = 'AGROUND',
  ENGAGED_IN_FISHING = 'ENGAGED_IN_FISHING',
  UNDER_WAY_SAILING = 'UNDER_WAY_SAILING',
  AIS_SART = 'AIS_SART',
  UNKNOWN = 'UNKNOWN',
}

export enum OperationalStatus {
  OPERATIONAL = 'OPERATIONAL',
  DEGRADED = 'DEGRADED',
  DAMAGED = 'DAMAGED',
  DESTROYED = 'DESTROYED',
  INACTIVE = 'INACTIVE',
  UNKNOWN = 'UNKNOWN',
}

export enum DamageAssessment {
  NONE = 'NONE',
  LIGHT = 'LIGHT',
  MODERATE = 'MODERATE',
  HEAVY = 'HEAVY',
  DESTROYED = 'DESTROYED',
  UNKNOWN = 'UNKNOWN',
}

export enum Affiliation {
  FRIENDLY = 'FRIENDLY',
  HOSTILE = 'HOSTILE',
  NEUTRAL = 'NEUTRAL',
  UNKNOWN = 'UNKNOWN',
  ASSUMED_FRIENDLY = 'ASSUMED_FRIENDLY',
  SUSPECT = 'SUSPECT',
  PENDING = 'PENDING',
}

export enum CharacterizationState {
  ASSESSED = 'ASSESSED',
  ASSUMED = 'ASSUMED',
  SUSPECTED = 'SUSPECTED',
  UNCHARACTERIZED = 'UNCHARACTERIZED',
}

/**
 * Ordered classification levels for comparison queries.
 * Lower index = lower classification.
 */
export const CLASSIFICATION_ORDER: readonly Classification[] = [
  Classification.UNCLASSIFIED,
  Classification.CONFIDENTIAL,
  Classification.SECRET,
  Classification.TOP_SECRET,
] as const;

/**
 * Returns the numeric rank of a classification level (0-based).
 */
export function classificationRank(classification: Classification): number {
  const index = CLASSIFICATION_ORDER.indexOf(classification);
  return index === -1 ? 0 : index;
}
