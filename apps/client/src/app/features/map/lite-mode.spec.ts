import { startsLite } from './lite-mode';

/** A matchMedia holder where only the listed queries match. */
function media(...matching: string[]): Pick<Window, 'matchMedia'> {
  return { matchMedia: ((q: string) => ({ matches: matching.includes(q) })) as unknown as Window['matchMedia'] };
}

describe('startsLite', () => {
  it('is true for a narrow screen', () => {
    expect(startsLite(media('(max-width: 768px)'))).toBe(true);
  });

  it('is true for a coarse pointer', () => {
    expect(startsLite(media('(pointer: coarse)'))).toBe(true);
  });

  it('is false for a desktop', () => {
    expect(startsLite(media())).toBe(false);
  });

  it('is false when matchMedia is missing or throws', () => {
    expect(startsLite({} as Pick<Window, 'matchMedia'>)).toBe(false);
    expect(
      startsLite({
        matchMedia: () => {
          throw new Error('nope');
        },
      }),
    ).toBe(false);
  });
});
