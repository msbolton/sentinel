import { formatAisEta, navStatusLabel, vesselCategory } from './vessel-format';

describe('vessel format', () => {
  it('maps AIS ship type codes to categories', () => {
    expect(vesselCategory(70)).toBe('cargo');
    expect(vesselCategory(79)).toBe('cargo');
    expect(vesselCategory(84)).toBe('tanker');
    expect(vesselCategory(60)).toBe('passenger');
    expect(vesselCategory(30)).toBe('fishing');
    expect(vesselCategory(40)).toBe('highSpeed');
    expect(vesselCategory(37)).toBe('pleasure');
    expect(vesselCategory(36)).toBe('pleasure');
    expect(vesselCategory(52)).toBe('special'); // tug
    expect(vesselCategory(35)).toBe('special'); // military
    expect(vesselCategory(0)).toBe('other');
    expect(vesselCategory(undefined)).toBe('other');
    expect(vesselCategory(99)).toBe('other');
  });

  it('writes the navigational status in words', () => {
    expect(navStatusLabel('UNDER_WAY_USING_ENGINE')).toBe('Under way (engine)');
    expect(navStatusLabel('MOORED')).toBe('Moored');
    expect(navStatusLabel('UNKNOWN')).toBeNull();
    expect(navStatusLabel(undefined)).toBeNull();
    expect(navStatusLabel('RESERVED_FOR_HSC')).toBe('reserved for hsc');
  });

  it('formats the yearless AIS ETA and full timestamps alike', () => {
    expect(formatAisEta('10-17T22:00')).toBe('17 Oct 22:00 UTC');
    expect(formatAisEta('01-05T06:30')).toBe('5 Jan 06:30 UTC');
    expect(formatAisEta('13-05T06:30')).toBeNull();
    expect(formatAisEta('2026-10-18T14:30:00Z')).toBe('18 Oct 14:30 UTC');
    expect(formatAisEta('')).toBeNull();
    expect(formatAisEta('soon')).toBe('soon');
  });
});
