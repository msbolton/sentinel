import { IconName } from '../../shared/components/icon.component';

export interface NavItem {
  path: string;
  label: string;
  icon: IconName;
  /** Single-key shortcut, pressed with no modifier outside a field. */
  key: string;
  /** Shown only to users holding one of these roles. */
  roles?: string[];
  /** Which live count the item carries, if any. */
  badge?: 'alerts' | 'findings';
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

export const NAV_GROUPS: NavGroup[] = [
  {
    label: 'Operate',
    items: [
      { path: '/map', label: 'Map', icon: 'globe', key: 'm' },
      { path: '/search', label: 'Search', icon: 'search', key: 's' },
      { path: '/alerts', label: 'Alerts', icon: 'bell', key: 'a', badge: 'alerts' },
      { path: '/findings', label: 'Findings', icon: 'activity', key: 'f', badge: 'findings' },
      { path: '/geofences', label: 'Geofences', icon: 'hexagon', key: 'g' },
    ],
  },
  {
    label: 'Analyse',
    items: [
      { path: '/link-graph', label: 'Link analysis', icon: 'link', key: 'l' },
      { path: '/timeline', label: 'Timeline', icon: 'clock', key: 't' },
      { path: '/locations', label: 'Locations', icon: 'map-pin', key: 'p' },
    ],
  },
  {
    label: 'Admin',
    items: [
      { path: '/connections', label: 'Connections', icon: 'rss', key: 'c', roles: ['sentinel-operator', 'sentinel-admin'] },
    ],
  },
];

const ROUTE_BY_KEY = new Map(NAV_GROUPS.flatMap((g) => g.items).map((item) => [item.key, item.path]));

type ShortcutEvent = Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'target'>;

/** The route a bare key press navigates to, or null when it is not a shortcut. */
export function routeForShortcut(event: ShortcutEvent): string | null {
  if (event.ctrlKey || event.metaKey || event.altKey) return null;
  if (isEditable(event.target)) return null;
  return ROUTE_BY_KEY.get(event.key.toLowerCase()) ?? null;
}

function isEditable(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false;
  const tag = target.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
  if ((target as HTMLElement).isContentEditable) return true;
  const editable = target.closest('[contenteditable]');
  return editable !== null && editable.getAttribute('contenteditable') !== 'false';
}
