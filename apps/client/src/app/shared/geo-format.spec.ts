import { formatAltitude, formatCoordinate, formatMgrs } from './geo-format';

describe('formatCoordinate', () => {
  it('writes latitude with four decimals and a hemisphere', () => {
    expect(formatCoordinate(25.276987, 'lat')).toBe('25.2770° N');
    expect(formatCoordinate(-33.8688, 'lat')).toBe('33.8688° S');
  });

  it('writes longitude with four decimals and a hemisphere', () => {
    expect(formatCoordinate(55.296249, 'lon')).toBe('55.2962° E');
    expect(formatCoordinate(-118.2437, 'lon')).toBe('118.2437° W');
  });

  it('treats zero as north and east', () => {
    expect(formatCoordinate(0, 'lat')).toBe('0.0000° N');
    expect(formatCoordinate(0, 'lon')).toBe('0.0000° E');
  });
});

describe('formatAltitude', () => {
  it('writes metres below ten kilometres', () => {
    expect(formatAltitude(0)).toBe('0 m');
    expect(formatAltitude(1234.6)).toBe('1235 m');
    expect(formatAltitude(9999)).toBe('9999 m');
  });

  it('writes kilometres from ten kilometres, with a decimal below a hundred', () => {
    expect(formatAltitude(10_000)).toBe('10.0 km');
    expect(formatAltitude(35_786_000)).toBe('35786 km');
    expect(formatAltitude(408_250)).toBe('408 km');
  });

  it('shows a dash for a missing value', () => {
    expect(formatAltitude(null)).toBe('—');
    expect(formatAltitude(undefined)).toBe('—');
    expect(formatAltitude(Number.NaN)).toBe('—');
  });
});

describe('formatMgrs', () => {
  it('formats MGRS with spaced groups at 10 m precision', () => {
    expect(formatMgrs(12.055, 44.8144)).toBe('38P MU 7979 3264');
  });

  it('returns a dash outside the UTM bands instead of throwing', () => {
    expect(formatMgrs(85, 10)).toBe('—');
    expect(formatMgrs(-85, 10)).toBe('—');
  });
});
