/**
 * Whether the map should start light: no terrain, no 3D buildings.
 *
 * True on a narrow screen or a coarse (touch) pointer, where terrain and
 * building tiles cost more than a phone can pay. It only sets the default; a
 * choice the user has saved always wins. A browser without matchMedia (or one
 * that throws) is treated as a desktop.
 */
export function startsLite(win: Pick<Window, 'matchMedia'> = window): boolean {
  try {
    if (typeof win?.matchMedia !== 'function') return false;
    return win.matchMedia('(max-width: 768px)').matches || win.matchMedia('(pointer: coarse)').matches;
  } catch {
    return false;
  }
}
