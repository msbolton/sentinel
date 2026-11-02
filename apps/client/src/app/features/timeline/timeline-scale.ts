import { TimelineEvent, TimeWindow } from '../../shared/models/track.model';

/** Stacking order of entity types in a timeline bar, bottom first. */
export const ENTITY_TYPE_ORDER: readonly string[] = [
  'AIRCRAFT',
  'VESSEL',
  'DRONE',
  'VEHICLE',
  'PERSON',
  'FACILITY',
  'UNIT',
  'EQUIPMENT',
  'SIGNAL',
  'SENSOR',
  'CYBER',
  'UNKNOWN',
];

const DAY_MS = 24 * 60 * 60 * 1000;

const pad2 = (n: number): string => String(n).padStart(2, '0');

function span(window: TimeWindow): number {
  return window.end.getTime() - window.start.getTime();
}

/** Horizontal position of `t` within a strip `width` wide; 0 for an empty window. */
export function timeToX(t: Date, window: TimeWindow, width: number): number {
  const total = span(window);
  if (total <= 0) return 0;
  return ((t.getTime() - window.start.getTime()) / total) * width;
}

/** The time at horizontal position `x`, clamped to the window. */
export function xToTime(x: number, window: TimeWindow, width: number): Date {
  const total = span(window);
  if (total <= 0 || width <= 0) return new Date(window.start.getTime());
  const fraction = Math.min(1, Math.max(0, x / width));
  return new Date(window.start.getTime() + fraction * total);
}

/** Evenly spaced axis labels (UTC): `HH:MM` under 24h, `DD HH:MM` otherwise. */
export function tickLabels(window: TimeWindow, count = 6): { x: number; label: string }[] {
  const total = span(window);
  const withDay = total >= DAY_MS;
  const ticks: { x: number; label: string }[] = [];
  for (let i = 0; i < count; i++) {
    const x = count > 1 ? i / (count - 1) : 0;
    // Whole milliseconds: Date truncates a fraction, so a tick on a minute could read a minute early.
    const offset = count > 1 ? Math.round((i * total) / (count - 1)) : 0;
    const t = new Date(window.start.getTime() + offset);
    const time = `${pad2(t.getUTCHours())}:${pad2(t.getUTCMinutes())}`;
    ticks.push({ x, label: withDay ? `${pad2(t.getUTCDate())} ${time}` : time });
  }
  return ticks;
}

export interface StackedBar {
  x: number;
  width: number;
  segments: { type: string; y: number; height: number }[];
}

function typeRank(type: string): number {
  const index = ENTITY_TYPE_ORDER.indexOf(type);
  return index === -1 ? ENTITY_TYPE_ORDER.length : index;
}

/**
 * Lays out one bar per bucket across `width`. Segment `y` is measured from the
 * top; the first type in ENTITY_TYPE_ORDER sits at the bottom, unlisted types last.
 * Heights are relative to the busiest bucket.
 */
export function stackBuckets(buckets: TimelineEvent[], height: number, width: number): StackedBar[] {
  if (buckets.length === 0) return [];
  const barWidth = width / buckets.length;
  const maxCount = Math.max(...buckets.map((b) => b.count));

  return buckets.map((bucket, i) => {
    const segments: StackedBar['segments'] = [];
    if (maxCount > 0 && bucket.count > 0) {
      const types = Object.keys(bucket.entityTypes)
        .filter((type) => bucket.entityTypes[type] > 0)
        .sort((a, b) => typeRank(a) - typeRank(b));
      let bottom = height;
      for (const type of types) {
        const segmentHeight = (bucket.entityTypes[type] / maxCount) * height;
        bottom -= segmentHeight;
        segments.push({ type, y: bottom, height: segmentHeight });
      }
    }
    return { x: i * barWidth, width: barWidth, segments };
  });
}

/** CSS colour for an entity type, falling back to the unknown colour. */
export function typeColorVar(type: string): string {
  return `var(--entity-${type.toLowerCase()}, var(--entity-unknown))`;
}
