export {
  EntityType,
  EntitySource,
  Classification,
  TrackEnvironment,
  TrackProcessingState,
  NavigationalStatus,
  OperationalStatus,
  DamageAssessment,
  Affiliation,
  CharacterizationState,
} from '@sentinel/proto-gen';
import { Classification } from '@sentinel/proto-gen';

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
