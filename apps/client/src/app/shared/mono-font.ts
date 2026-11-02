let cached: string | null = null;

/**
 * The resolved `--font-mono` stack for code that draws on a canvas or hands a
 * font string to Cesium, where `var(--font-mono)` does not work. Read once,
 * the first time it is asked for, then reused; falls back to `monospace`
 * where the stylesheet is not loaded (tests).
 */
export function monoFont(): string {
  if (cached) return cached;
  if (typeof document === 'undefined') return 'monospace';
  const value = getComputedStyle(document.documentElement).getPropertyValue('--font-mono').trim();
  if (value) cached = value;
  return value || 'monospace';
}
