import { CLASSIFICATION_LEVELS, levelsUpTo, normalizeClearance, rank } from './classification';

describe('classification helpers', () => {
  it('orders the levels lowest first', () => {
    expect(CLASSIFICATION_LEVELS).toEqual(['UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET']);
    expect(rank('UNCLASSIFIED')).toBe(0);
    expect(rank('TOP_SECRET')).toBe(3);
    expect(rank('SECRET')).toBeGreaterThan(rank('CONFIDENTIAL'));
  });

  it('lists every level up to the clearance', () => {
    expect(levelsUpTo('UNCLASSIFIED')).toEqual(['UNCLASSIFIED']);
    expect(levelsUpTo('SECRET')).toEqual(['UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET']);
    expect(levelsUpTo('TOP_SECRET')).toEqual([...CLASSIFICATION_LEVELS]);
    expect(levelsUpTo(undefined)).toEqual(['UNCLASSIFIED']);
  });

  it('normalises the spellings seen in tokens and older code', () => {
    expect(normalizeClearance('TOP SECRET')).toBe('TOP_SECRET');
    expect(normalizeClearance('top_secret')).toBe('TOP_SECRET');
    expect(normalizeClearance('classification-ts')).toBe('TOP_SECRET');
    expect(normalizeClearance('classification-s')).toBe('SECRET');
    expect(normalizeClearance('classification-c')).toBe('CONFIDENTIAL');
    expect(normalizeClearance('classification-u')).toBe('UNCLASSIFIED');
    expect(normalizeClearance('Secret')).toBe('SECRET');
    expect(normalizeClearance('')).toBe('UNCLASSIFIED');
    expect(normalizeClearance(null)).toBe('UNCLASSIFIED');
    expect(normalizeClearance('nonsense')).toBe('UNCLASSIFIED');
  });
});
