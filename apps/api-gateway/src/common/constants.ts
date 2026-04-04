/** Application-wide constants */

export const APP_NAME = 'SENTINEL';
export const API_VERSION = 'v1';
export const API_PREFIX = `api/${API_VERSION}`;

/** Default pagination */
export const DEFAULT_PAGE_SIZE = 50;
export const MAX_PAGE_SIZE = 1000;

/** Redis key prefixes */
export const RedisKeys = {
  ENTITY_POSITIONS: 'sentinel:positions',
  VIEWPORTS: 'sentinel:viewports',
  SESSIONS: 'sentinel:sessions',
  WS_REGISTRY: 'sentinel:ws-registry',
  ENTITY_CACHE: 'sentinel:entity-cache',
  ALERT_COUNT: 'sentinel:alert-count',
} as const;

/** Entity type display colors (for frontend rendering) */
export const EntityTypeColors: Record<string, string> = {
  PERSON: '#4A90D9',
  VEHICLE: '#50C878',
  VESSEL: '#00CED1',
  AIRCRAFT: '#FFD700',
  FACILITY: '#DC3545',
  EQUIPMENT: '#9B59B6',
  UNIT: '#E67E22',
  SIGNAL: '#1ABC9C',
  CYBER: '#E74C3C',
  UNKNOWN: '#95A5A6',
};

/** Alert severity colors */
export const AlertSeverityColors: Record<string, string> = {
  CRITICAL: '#DC3545',
  HIGH: '#FD7E14',
  MEDIUM: '#FFC107',
  LOW: '#4A90D9',
  INFO: '#6C757D',
};

/** Track segment gap threshold (ms) - gaps larger create new segment */
export const TRACK_SEGMENT_GAP_MS = 30 * 60 * 1000; // 30 minutes

/** Viewport update debounce (ms) */
export const VIEWPORT_UPDATE_DEBOUNCE_MS = 300;

/** Maximum track trail points to render on map */
export const MAX_TRACK_TRAIL_POINTS = 100;
