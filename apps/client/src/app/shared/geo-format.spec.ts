import { formatCoordinate } from './geo-format';

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
