import { SetMetadata } from '@nestjs/common';

/**
 * Metadata key used to store the required classification level on route handlers.
 */
export const CLASSIFICATION_KEY = 'sentinel:classification';

/**
 * Classification levels aligned with the proto enum.
 * Ordered from lowest to highest clearance.
 */
export type ClassificationLevel =
  | 'UNCLASSIFIED'
  | 'CONFIDENTIAL'
  | 'SECRET'
  | 'TOP_SECRET';

/**
 * Numeric hierarchy for classification level comparisons.
 * A user must have a level >= the required level to access a resource.
 */
export const CLASSIFICATION_HIERARCHY: Record<ClassificationLevel, number> = {
  UNCLASSIFIED: 0,
  CONFIDENTIAL: 1,
  SECRET: 2,
  TOP_SECRET: 3,
};

/**
 * Decorator that enforces a minimum classification clearance level on a route.
 * The authenticated user's classification_level claim must meet or exceed
 * the specified level.
 *
 * @example
 * ```typescript
 * @Classification('SECRET')
 * @Get('sigint-tracks')
 * getSigintTracks() { ... }
 * ```
 */
export const Classification = (level: ClassificationLevel) =>
  SetMetadata(CLASSIFICATION_KEY, level);
