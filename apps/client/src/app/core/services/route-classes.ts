/** The router URL without its query string or fragment. */
function pathOf(url: string): string {
  return url.replace(/[?#].*$/, '');
}

/** Sign-in and demo-start pages: rendered without the app shell. */
export function isFullScreenRoute(url: string): boolean {
  return ['/login', '/register', '/demo'].includes(pathOf(url));
}

/** Full pages (settings, connections, admin) that replace the map. */
export function isPageRoute(url: string): boolean {
  const path = pathOf(url);
  return ['/admin', '/settings', '/connections', '/feeds'].some((p) => path.startsWith(p));
}

/** The timeline: a strip along the bottom of the map, not a right-hand panel. */
export function isTimelineRoute(url: string): boolean {
  return pathOf(url).startsWith('/timeline');
}

/**
 * True when a route panel (alerts, search, …) covers the right edge of the map,
 * so the overlays on that side move clear of it.
 */
export function isPanelRoute(url: string): boolean {
  const path = pathOf(url);
  if (path === '' || path === '/' || path === '/map') return false;
  return !isPageRoute(path) && !isFullScreenRoute(path) && !isTimelineRoute(path);
}
