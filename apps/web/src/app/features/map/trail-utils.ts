export interface TrailPoint {
  lat: number;
  lon: number;
  alt: number;
}

export class CircularBuffer<T> {
  private buffer: (T | undefined)[];
  private head = 0;
  private count = 0;

  constructor(private readonly capacity: number) {
    this.buffer = new Array(capacity);
  }

  push(item: T): void {
    this.buffer[this.head] = item;
    this.head = (this.head + 1) % this.capacity;
    if (this.count < this.capacity) this.count++;
  }

  get length(): number {
    return this.count;
  }

  toArray(): T[] {
    if (this.count === 0) return [];
    const result: T[] = new Array(this.count);
    const start = this.count < this.capacity ? 0 : this.head;
    for (let i = 0; i < this.count; i++) {
      result[i] = this.buffer[(start + i) % this.capacity] as T;
    }
    return result;
  }
}

export interface DecimationConfig {
  HIGH_ALT_THRESHOLD: number;
  HIGH_ALT_STRIDE: number;
  MID_ALT_THRESHOLD: number;
  MID_ALT_STRIDE: number;
  LOW_ALT_STRIDE: number;
}

/**
 * Return every Nth point from a trail based on camera altitude.
 * Always includes the last point (current position).
 */
export function decimateTrail(
  trail: TrailPoint[],
  cameraAltitude: number,
  config: DecimationConfig,
): TrailPoint[] {
  if (trail.length <= 2) return trail;

  let stride: number;
  if (cameraAltitude > config.HIGH_ALT_THRESHOLD) {
    stride = config.HIGH_ALT_STRIDE;
  } else if (cameraAltitude > config.MID_ALT_THRESHOLD) {
    stride = config.MID_ALT_STRIDE;
  } else {
    stride = config.LOW_ALT_STRIDE;
  }

  if (stride <= 1) return trail;

  const result: TrailPoint[] = [];
  for (let i = 0; i < trail.length; i += stride) {
    result.push(trail[i]);
  }

  const last = trail[trail.length - 1];
  if (result[result.length - 1] !== last) {
    result.push(last);
  }

  return result;
}
