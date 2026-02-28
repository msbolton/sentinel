/** Format a Date as ISO 8601 string */
export function toISOString(date: Date): string {
  return date.toISOString();
}

/** Parse ISO string to Date */
export function fromISOString(iso: string): Date {
  return new Date(iso);
}

/** Get milliseconds between two dates */
export function msBetween(a: Date, b: Date): number {
  return Math.abs(b.getTime() - a.getTime());
}

/** Check if a date is within a time range */
export function isWithinTimeRange(
  date: Date,
  start: Date,
  end: Date,
): boolean {
  const t = date.getTime();
  return t >= start.getTime() && t <= end.getTime();
}

/** Get a human-readable relative time string (e.g., "5 minutes ago") */
export function relativeTime(date: Date): string {
  const now = Date.now();
  const diffMs = now - date.getTime();
  const diffSec = Math.floor(diffMs / 1000);

  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour}h ago`;
  const diffDay = Math.floor(diffHour / 24);
  return `${diffDay}d ago`;
}

/** Format duration in milliseconds to human-readable */
export function formatDuration(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (days > 0) return `${days}d ${hours % 24}h`;
  if (hours > 0) return `${hours}h ${minutes % 60}m`;
  if (minutes > 0) return `${minutes}m ${seconds % 60}s`;
  return `${seconds}s`;
}
