import { TimelineEvent, TimeWindow } from '../../shared/models/track.model';
import {
  ENTITY_TYPE_ORDER,
  stackBuckets,
  tickLabels,
  timeToX,
  typeColorVar,
  xToTime,
} from './timeline-scale';

const HOUR = 3_600_000;
const start = new Date(Date.UTC(2026, 10, 2, 6, 0, 0));
const window6h: TimeWindow = { start, end: new Date(start.getTime() + 6 * HOUR) };
const window7d: TimeWindow = { start, end: new Date(start.getTime() + 7 * 24 * HOUR) };

const bucket = (count: number, entityTypes: Record<string, number>): TimelineEvent => ({
  timestamp: '2026-11-02T06:00:00Z',
  count,
  entityTypes,
});

describe('timeToX / xToTime', () => {
  it('maps the ends and the middle of the window onto the width', () => {
    expect(timeToX(window6h.start, window6h, 600)).toBe(0);
    expect(timeToX(new Date(start.getTime() + 3 * HOUR), window6h, 600)).toBe(300);
    expect(timeToX(window6h.end, window6h, 600)).toBe(600);
  });

  it('round-trips through xToTime', () => {
    for (const x of [0, 150, 300, 600]) {
      const t = xToTime(x, window6h, 600);
      expect(timeToX(t, window6h, 600)).toBeCloseTo(x, 6);
    }
    expect(xToTime(300, window6h, 600).getTime()).toBe(start.getTime() + 3 * HOUR);
  });

  it('clamps xToTime to the window', () => {
    expect(xToTime(-50, window6h, 600).getTime()).toBe(window6h.start.getTime());
    expect(xToTime(9999, window6h, 600).getTime()).toBe(window6h.end.getTime());
  });

  it('does not divide by zero for an empty window or width', () => {
    const empty: TimeWindow = { start, end: start };
    expect(timeToX(start, empty, 600)).toBe(0);
    expect(xToTime(100, empty, 600).getTime()).toBe(start.getTime());
    expect(xToTime(100, window6h, 0).getTime()).toBe(start.getTime());
  });
});

describe('tickLabels', () => {
  it('gives six HH:MM labels across a 6h window, evenly spaced from 0 to 1', () => {
    const ticks = tickLabels(window6h);
    expect(ticks.map((t) => t.label)).toEqual(['06:00', '07:12', '08:24', '09:36', '10:48', '12:00']);
    expect(ticks[0].x).toBe(0);
    expect(ticks[5].x).toBe(1);
    expect(ticks[1].x).toBeCloseTo(0.2, 9);
  });

  it('uses DD HH:MM for windows of a day or more', () => {
    const ticks = tickLabels(window7d);
    expect(ticks).toHaveLength(6);
    expect(ticks[0].label).toBe('02 06:00');
    expect(ticks[5].label).toBe('09 06:00');
    for (const t of ticks) expect(t.label).toMatch(/^\d{2} \d{2}:\d{2}$/);
  });

  it('honours the requested count', () => {
    expect(tickLabels(window6h, 3).map((t) => t.label)).toEqual(['06:00', '09:00', '12:00']);
  });
});

describe('stackBuckets', () => {
  const buckets = [
    bucket(4, { AIRCRAFT: 3, VESSEL: 1 }),
    bucket(2, { VESSEL: 2 }),
  ];

  it('lays bars out across the width', () => {
    const bars = stackBuckets(buckets, 40, 100);
    expect(bars.map((b) => [b.x, b.width])).toEqual([
      [0, 50],
      [50, 50],
    ]);
  });

  it('stacks the first type at the bottom and sizes against the busiest bucket', () => {
    const [first, second] = stackBuckets(buckets, 40, 100);

    expect(first.segments).toEqual([
      { type: 'AIRCRAFT', y: 10, height: 30 },
      { type: 'VESSEL', y: 0, height: 10 },
    ]);
    const aircraft = first.segments.find((s) => s.type === 'AIRCRAFT')!;
    const vessel = first.segments.find((s) => s.type === 'VESSEL')!;
    expect(aircraft.y).toBeGreaterThan(vessel.y);
    expect(first.segments.reduce((sum, s) => sum + s.height, 0)).toBe(40);

    expect(second.segments).toEqual([{ type: 'VESSEL', y: 20, height: 20 }]);
  });

  it('gives an empty bucket no segments', () => {
    const bars = stackBuckets([bucket(0, {}), bucket(2, { DRONE: 2 })], 40, 100);
    expect(bars[0].segments).toEqual([]);
    expect(bars[1].segments).toEqual([{ type: 'DRONE', y: 0, height: 40 }]);
  });

  it('gives no segments anywhere when every bucket is empty', () => {
    const bars = stackBuckets([bucket(0, {}), bucket(0, {})], 40, 100);
    expect(bars).toHaveLength(2);
    expect(bars.every((b) => b.segments.length === 0)).toBe(true);
  });

  it('returns nothing for no buckets', () => {
    expect(stackBuckets([], 40, 100)).toEqual([]);
  });

  it('puts types outside the order list last (topmost)', () => {
    const [bar] = stackBuckets([bucket(3, { MYSTERY: 1, UNKNOWN: 1, AIRCRAFT: 1 })], 30, 10);
    expect(bar.segments.map((s) => s.type)).toEqual(['AIRCRAFT', 'UNKNOWN', 'MYSTERY']);
    expect(bar.segments.map((s) => s.y)).toEqual([20, 10, 0]);
  });
});

describe('entity types', () => {
  it('orders the known types with AIRCRAFT first and UNKNOWN last', () => {
    expect(ENTITY_TYPE_ORDER[0]).toBe('AIRCRAFT');
    expect(ENTITY_TYPE_ORDER[ENTITY_TYPE_ORDER.length - 1]).toBe('UNKNOWN');
    expect(ENTITY_TYPE_ORDER).toHaveLength(12);
  });

  it('builds a colour variable that falls back to unknown', () => {
    expect(typeColorVar('AIRCRAFT')).toBe('var(--entity-aircraft, var(--entity-unknown))');
  });
});
