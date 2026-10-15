import { CircularBuffer, decimateTrail, TrailPoint } from './trail-utils';
import { TRACK_TRAIL_CONFIG } from './cesium-config';

const CONFIG = TRACK_TRAIL_CONFIG.decimation;

function makeTrail(n: number): TrailPoint[] {
  return Array.from({ length: n }, (_, i) => ({ lat: i, lon: i, alt: 0 }));
}

describe('CircularBuffer', () => {
  it('should store items up to capacity', () => {
    const buf = new CircularBuffer<number>(5);
    for (let i = 0; i < 5; i++) buf.push(i);
    expect(buf.length).toBe(5);
    expect(buf.toArray()).toEqual([0, 1, 2, 3, 4]);
  });

  it('should overwrite oldest items when full', () => {
    const buf = new CircularBuffer<number>(3);
    for (let i = 0; i < 5; i++) buf.push(i);
    expect(buf.length).toBe(3);
    expect(buf.toArray()).toEqual([2, 3, 4]);
  });

  it('should return empty array when empty', () => {
    const buf = new CircularBuffer<number>(10);
    expect(buf.toArray()).toEqual([]);
    expect(buf.length).toBe(0);
  });
});

describe('decimateTrail', () => {
  it('should return all points at low altitude', () => {
    const trail = makeTrail(100);
    const result = decimateTrail(trail, 400_000, CONFIG);
    expect(result.length).toBe(100);
  });

  it('should return every 5th point at mid altitude', () => {
    const trail = makeTrail(100);
    const result = decimateTrail(trail, 1_000_000, CONFIG);
    expect(result.length).toBe(21); // 0,5,10,...95 = 20 + last(99) = 21
    expect(result[0]).toBe(trail[0]);
    expect(result[result.length - 1]).toBe(trail[99]);
  });

  it('should return every 20th point at high altitude', () => {
    const trail = makeTrail(300);
    const result = decimateTrail(trail, 10_000_000, CONFIG);
    expect(result.length).toBe(16); // 0,20,...280 = 15 + last(299) = 16
    expect(result[0]).toBe(trail[0]);
    expect(result[result.length - 1]).toBe(trail[299]);
  });

  it('should always include the last point', () => {
    const trail = makeTrail(50);
    const result = decimateTrail(trail, 10_000_000, CONFIG);
    expect(result[result.length - 1]).toBe(trail[49]);
  });

  it('should return short trails unchanged', () => {
    expect(decimateTrail(makeTrail(1), 10_000_000, CONFIG).length).toBe(1);
    expect(decimateTrail(makeTrail(2), 10_000_000, CONFIG).length).toBe(2);
  });

  it('should not duplicate last point when stride lands on it', () => {
    const trail = makeTrail(21); // stride 20: indices 0,20 -- 20 IS the last
    const result = decimateTrail(trail, 10_000_000, CONFIG);
    expect(result.length).toBe(2);
  });
});
