import { entityLabel } from './entity-label';

describe('entityLabel', () => {
  it('leaves a live entity alone', () => {
    expect(entityLabel('Ship', false)).toBe('Ship');
  });

  it('treats an absent exercise flag as live', () => {
    expect(entityLabel('Ship', undefined)).toBe('Ship');
  });

  it('marks an exercise entity', () => {
    expect(entityLabel('Ship', true)).toBe('Ship · EX');
  });

  it('puts the suffix before the exercise mark', () => {
    expect(entityLabel('ISS', true, '408 km')).toBe('ISS · 408 km · EX');
    expect(entityLabel('ISS', false, '408 km')).toBe('ISS · 408 km');
  });
});
