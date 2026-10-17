/**
 * Classification levels as the gateway spells them (`x-sentinel-classification`,
 * `feed.config.classification`, `entity.classification`), lowest first.
 */
export const CLASSIFICATION_LEVELS = ['UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET'] as const;

export type ClassificationLevel = (typeof CLASSIFICATION_LEVELS)[number];

/** Position of a level in the ordering; -1 for anything that is not a level. */
export function rank(level: string | null | undefined): number {
  return CLASSIFICATION_LEVELS.indexOf(normalizeClearance(level));
}

/** Every level the holder of `level` may use: their own and everything below it. */
export function levelsUpTo(level: string | null | undefined): ClassificationLevel[] {
  return CLASSIFICATION_LEVELS.slice(0, rank(level) + 1);
}

/**
 * Turns the spellings seen in tokens and older code ('TOP SECRET',
 * 'classification-ts', 'top_secret', 'S') into a level; anything unknown is
 * UNCLASSIFIED.
 */
export function normalizeClearance(claim: string | null | undefined): ClassificationLevel {
  if (!claim) {
    return 'UNCLASSIFIED';
  }
  const key = claim
    .trim()
    .toUpperCase()
    .replace(/^CLASSIFICATION[-_]/, '')
    .replace(/[\s-]+/g, '_');
  switch (key) {
    case 'TS':
    case 'TOP_SECRET':
      return 'TOP_SECRET';
    case 'S':
    case 'SECRET':
      return 'SECRET';
    case 'C':
    case 'CONFIDENTIAL':
      return 'CONFIDENTIAL';
    default:
      return 'UNCLASSIFIED';
  }
}
