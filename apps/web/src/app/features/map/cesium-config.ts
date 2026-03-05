import { EntityType } from '../../shared/models/entity.model';

/**
 * Configure Cesium base URL for asset loading.
 * Must be called before any Cesium classes are instantiated.
 */
export function configureCesium(): void {
  (window as any).CESIUM_BASE_URL = '/assets/cesium';

  // Disable optional WASM features to reduce memory usage
  // This prevents WebAssembly out-of-memory errors in some browsers
  if (typeof window !== 'undefined') {
    (window as any).CESIUM_DISABLE_DRACO = true;
    (window as any).CESIUM_DISABLE_BASIS = true;
  }
}

/** Default Cesium Viewer configuration options */
export const CESIUM_VIEWER_OPTIONS = {
  animation: false,
  timeline: false,
  baseLayerPicker: false,
  fullscreenButton: false,
  geocoder: false,
  homeButton: false,
  infoBox: false,
  sceneModePicker: false,
  selectionIndicator: true,
  navigationHelpButton: false,
  navigationInstructionsInitiallyVisible: false,
  creditContainer: undefined as any,
  orderIndependentTranslucency: false,
  contextOptions: {
    webgl: {
      alpha: true,
      depth: true,
      stencil: false,
      antialias: true,
      powerPreference: 'high-performance' as const,
    },
  },
  requestRenderMode: true,
  maximumRenderTimeChange: Infinity,
};

/** Entity type to Cesium Color mapping */
export const ENTITY_TYPE_COLORS: Record<string, { red: number; green: number; blue: number; alpha: number }> = {
  [EntityType.PERSON]: { red: 0.231, green: 0.510, blue: 0.965, alpha: 1.0 },      // Blue
  [EntityType.VEHICLE]: { red: 0.063, green: 0.725, blue: 0.506, alpha: 1.0 },      // Green
  [EntityType.VESSEL]: { red: 0.024, green: 0.714, blue: 0.831, alpha: 1.0 },       // Cyan
  [EntityType.AIRCRAFT]: { red: 0.961, green: 0.620, blue: 0.043, alpha: 1.0 },     // Yellow
  [EntityType.FACILITY]: { red: 0.937, green: 0.267, blue: 0.267, alpha: 1.0 },     // Red
  [EntityType.EQUIPMENT]: { red: 0.612, green: 0.639, blue: 0.686, alpha: 1.0 },    // Gray
  [EntityType.UNIT]: { red: 0.063, green: 0.725, blue: 0.506, alpha: 1.0 },         // Green
  [EntityType.SIGNAL]: { red: 0.545, green: 0.361, blue: 0.965, alpha: 1.0 },       // Purple
  [EntityType.CYBER]: { red: 0.545, green: 0.361, blue: 0.965, alpha: 1.0 },        // Purple
  [EntityType.SENSOR]: { red: 0.545, green: 0.361, blue: 0.965, alpha: 1.0 },        // Purple
  [EntityType.SATELLITE]: { red: 0.96, green: 0.96, blue: 0.96, alpha: 1.0 },        // White
  [EntityType.UNKNOWN]: { red: 0.353, green: 0.416, blue: 0.502, alpha: 1.0 },       // Muted (#5a6a80)
};

/** Entity type to icon/symbol character mapping (fallback when MIL-STD-2525D not available) */
export const ENTITY_TYPE_ICONS: Record<string, string> = {
  [EntityType.PERSON]: '\u{1F464}',      // bust in silhouette
  [EntityType.VEHICLE]: '\u{1F697}',     // automobile
  [EntityType.VESSEL]: '\u{1F6A2}',      // ship
  [EntityType.AIRCRAFT]: '\u{2708}',     // airplane
  [EntityType.FACILITY]: '\u{1F3E2}',    // office building
  [EntityType.EQUIPMENT]: '\u{1F527}',   // wrench
  [EntityType.UNIT]: '\u{1F396}',        // military medal
  [EntityType.SIGNAL]: '\u{1F4E1}',      // satellite antenna
  [EntityType.CYBER]: '\u{1F4BB}',       // laptop
  [EntityType.SENSOR]: '\u{1F4E1}',      // satellite antenna
  [EntityType.SATELLITE]: '\u{1F6F0}',   // satellite
  [EntityType.UNKNOWN]: '\u{2753}',      // question mark
};

/** Entity type to billboard pin path mapping */
export const ENTITY_TYPE_PIN_COLORS: Record<string, string> = {
  [EntityType.PERSON]: '#3b82f6',
  [EntityType.VEHICLE]: '#10b981',
  [EntityType.VESSEL]: '#06b6d4',
  [EntityType.AIRCRAFT]: '#f59e0b',
  [EntityType.FACILITY]: '#ef4444',
  [EntityType.EQUIPMENT]: '#9ca3af',
  [EntityType.UNIT]: '#10b981',
  [EntityType.SIGNAL]: '#8b5cf6',
  [EntityType.CYBER]: '#8b5cf6',
  [EntityType.SENSOR]: '#8b5cf6',
  [EntityType.SATELLITE]: '#f5f5f5',
  [EntityType.UNKNOWN]: '#5a6a80',
};

/** Default camera position (centered on world) */
export const DEFAULT_CAMERA_POSITION = {
  longitude: 0,
  latitude: 20,
  height: 20000000,
};

/** Track trail configuration */
export const TRACK_TRAIL_CONFIG = {
  maxPoints: 50,
  width: 2,
  trailOpacity: 0.4,
};
