import { EntityType } from '../../shared/models/entity.model';

/**
 * Configure Cesium base URL for asset loading.
 * Must be called before any Cesium classes are instantiated.
 */
export function configureCesium(): void {
  (window as any).CESIUM_BASE_URL = '/assets/cesium';

  // Disable Basis Universal texture compression (not needed)
  // Note: Draco must remain enabled for Google Photorealistic 3D Tiles
  if (typeof window !== 'undefined') {
    (window as any).CESIUM_DISABLE_BASIS = true;
  }
}

/** Default Cesium Viewer configuration options */
export const CESIUM_VIEWER_OPTIONS = {
  animation: false,
  timeline: false,
  baseLayerPicker: true,
  fullscreenButton: false,
  geocoder: true,
  homeButton: false,
  infoBox: false,
  sceneModePicker: true,
  selectionIndicator: true,
  navigationHelpButton: false,
  navigationInstructionsInitiallyVisible: false,
  creditContainer: undefined as any,
  orderIndependentTranslucency: false,
  shadows: false,
  skyBox: false as any,
  skyAtmosphere: false as any,
  contextOptions: {
    webgl: {
      alpha: true,
      depth: true,
      stencil: false,
      antialias: false,
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
  [EntityType.AIRCRAFT]: { red: 0.96, green: 0.96, blue: 0.96, alpha: 1.0 },         // White
  [EntityType.DRONE]: { red: 0.961, green: 0.537, blue: 0.161, alpha: 1.0 },       // Orange
  [EntityType.FACILITY]: { red: 0.937, green: 0.267, blue: 0.267, alpha: 1.0 },     // Red
  [EntityType.EQUIPMENT]: { red: 0.612, green: 0.639, blue: 0.686, alpha: 1.0 },    // Gray
  [EntityType.UNIT]: { red: 0.063, green: 0.725, blue: 0.506, alpha: 1.0 },         // Green
  [EntityType.SIGNAL]: { red: 0.545, green: 0.361, blue: 0.965, alpha: 1.0 },       // Purple
  [EntityType.CYBER]: { red: 0.545, green: 0.361, blue: 0.965, alpha: 1.0 },        // Purple
  [EntityType.SENSOR]: { red: 0.545, green: 0.361, blue: 0.965, alpha: 1.0 },        // Purple
  [EntityType.SATELLITE]: { red: 0.612, green: 0.639, blue: 0.686, alpha: 1.0 },      // Gray
  [EntityType.UNKNOWN]: { red: 0.353, green: 0.416, blue: 0.502, alpha: 1.0 },       // Muted (#5a6a80)
};

/** Entity type to icon/symbol character mapping (fallback when MIL-STD-2525D not available) */
export const ENTITY_TYPE_ICONS: Record<string, string> = {
  [EntityType.PERSON]: '\u{1F464}',      // bust in silhouette
  [EntityType.VEHICLE]: '\u{1F697}',     // automobile
  [EntityType.VESSEL]: '\u{1F6A2}',      // ship
  [EntityType.AIRCRAFT]: '\u{2708}',     // airplane
  [EntityType.DRONE]: '\u{1F681}',       // helicopter (drone proxy)
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
  [EntityType.AIRCRAFT]: '#f5f5f5',
  [EntityType.DRONE]: '#f5892a',
  [EntityType.FACILITY]: '#ef4444',
  [EntityType.EQUIPMENT]: '#9ca3af',
  [EntityType.UNIT]: '#10b981',
  [EntityType.SIGNAL]: '#8b5cf6',
  [EntityType.CYBER]: '#8b5cf6',
  [EntityType.SENSOR]: '#8b5cf6',
  [EntityType.SATELLITE]: '#9ca3af',
  [EntityType.UNKNOWN]: '#5a6a80',
};

/** Default camera position (centered on world) */
export const DEFAULT_CAMERA_POSITION = {
  longitude: 0,
  latitude: 20,
  height: 20000000,
};

/** SVG icon markup per entity type — white silhouettes on transparent, tinted by Cesium billboard color */
export const ENTITY_TYPE_BILLBOARD_SVGS: Record<string, string> = {
  [EntityType.AIRCRAFT]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 559.185 559.185" width="64" height="64"><path fill="white" stroke="black" stroke-width="12" stroke-linejoin="round" d="M325.303,181.69V81.634C325.303,54.584,303.375,0,276.324,0c-27.056,0-48.984,54.584-48.984,81.634v103.973c-71.341,23.8-186.134,67.442-186.134,101.916c0,9.352,0,16.163,0,21.059c0,3.605,2.864,5.937,6.396,5.202l179.738-37.411v182.131c-43.697,27.332-99.597,66.017-99.597,85.007c0,4.4,0,7.901,0,10.661c0,3.604,2.815,5.747,6.292,4.78l138.036-38.544c3.47-0.974,9.094-0.949,12.571,0.043l133.991,38.451c3.464,0.998,6.272-1.126,6.272-4.736c0-2.767,0-6.261,0-10.661c0-19.395-56.731-59.321-99.597-86.732V273.907l186.281,39.847c3.531,0.753,6.389-1.561,6.389-5.165c0-4.896,0-11.708,0-21.06C517.967,251.067,393.021,204.34,325.303,181.69z"/></svg>`,
  [EntityType.DRONE]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="64" height="64" fill="none" stroke="white" stroke-miterlimit="10" stroke-width="1.91"><path d="M4.84,8.18A3.34,3.34,0,1,1,8.18,4.84"/><path d="M8.18,19.16a3.34,3.34,0,1,1-3.34-3.34"/><path d="M15.82,4.84a3.34,3.34,0,1,1,3.34,3.34"/><path d="M19.16,15.82a3.34,3.34,0,1,1-3.34,3.34"/><line x1="19.64" y1="19.64" x2="14.86" y2="14.86"/><line x1="9.14" y1="9.14" x2="4.36" y2="4.36"/><line x1="9.14" y1="14.86" x2="4.36" y2="19.64"/><line x1="19.64" y1="4.36" x2="14.86" y2="9.14"/><path d="M14.86,9.14v5.72a2.86,2.86,0,1,1-5.72,0V9.14a2.86,2.86,0,1,1,5.72,0Z"/></svg>`,
  [EntityType.VEHICLE]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><path fill="white" d="M8 28l2-8h28l2 8v6H8v-6zm4 2a2 2 0 1 0 0 4 2 2 0 0 0 0-4zm24 0a2 2 0 1 0 0 4 2 2 0 0 0 0-4zM14 20l2-6h16l2 6H14z"/></svg>`,
  // A hull seen from above, bow up, so it can turn with the vessel's heading.
  [EntityType.VESSEL]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><path fill="white" stroke="black" stroke-width="2" stroke-linejoin="round" d="M24 3C30 10 32 16 32 22V41C32 43.5 30.5 45 28 45H20C17.5 45 16 43.5 16 41V22C16 16 18 10 24 3Z"/><rect x="20" y="27" width="8" height="8" rx="1" fill="black" fill-opacity="0.35"/></svg>`,
  // A spacecraft seen from above: bus, two solar panels on booms, a mast and
  // a dish, tilted so it reads as a satellite rather than a ground station.
  [EntityType.SATELLITE]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="64" height="64"><g fill="white" transform="rotate(-40 32 32)"><rect x="25" y="23" width="14" height="18" rx="2.5"/><rect x="3" y="27" width="17" height="10" rx="1.5"/><rect x="44" y="27" width="17" height="10" rx="1.5"/><rect x="20" y="31" width="5" height="2"/><rect x="39" y="31" width="5" height="2"/><rect x="31" y="11" width="2" height="12"/><path d="M23 11 a9 4.5 0 0 0 18 0 z"/><circle cx="32" cy="46" r="2.5"/></g></svg>`,
  [EntityType.PERSON]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><circle cx="24" cy="14" r="7" fill="white"/><path fill="white" d="M12 38c0-8 5.4-14 12-14s12 6 12 14H12z"/></svg>`,
  [EntityType.FACILITY]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><path fill="white" d="M8 40V16l16-8 16 8v24H8zm8-18v4h4v-4h-4zm12 0v4h4v-4h-4zm-12 8v4h4v-4h-4zm12 0v4h4v-4h-4z"/></svg>`,
  [EntityType.UNKNOWN]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><circle cx="24" cy="24" r="10" fill="white"/></svg>`,
};

/** Green variant of the aircraft SVG for military (IntelSky) aircraft */
export const MILITARY_AIRCRAFT_BILLBOARD_SVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 559.185 559.185" width="64" height="64"><path fill="#22c55e" stroke="black" stroke-width="12" stroke-linejoin="round" d="M325.303,181.69V81.634C325.303,54.584,303.375,0,276.324,0c-27.056,0-48.984,54.584-48.984,81.634v103.973c-71.341,23.8-186.134,67.442-186.134,101.916c0,9.352,0,16.163,0,21.059c0,3.605,2.864,5.937,6.396,5.202l179.738-37.411v182.131c-43.697,27.332-99.597,66.017-99.597,85.007c0,4.4,0,7.901,0,10.661c0,3.604,2.815,5.747,6.292,4.78l138.036-38.544c3.47-0.974,9.094-0.949,12.571,0.043l133.991,38.451c3.464,0.998,6.272-1.126,6.272-4.736c0-2.767,0-6.261,0-10.661c0-19.395-56.731-59.321-99.597-86.732V273.907l186.281,39.847c3.531,0.753,6.389-1.561,6.389-5.165c0-4.896,0-11.708,0-21.06C517.967,251.067,393.021,204.34,325.303,181.69z"/></svg>`;

/** Convert SVG string to data URL for Cesium billboard */
export function svgToDataUrl(svg: string): string {
  return 'data:image/svg+xml;base64,' + btoa(svg);
}

/** Entity types whose billboard should rotate to match heading */
export const HEADING_ROTATED_TYPES = new Set<string>([
  EntityType.AIRCRAFT,
  EntityType.DRONE,
  EntityType.VESSEL,
]);

/** Billboard/label LOD scaling — fade and shrink at distance */
export const BILLBOARD_SCALE_BY_DISTANCE = [1_000, 1.0, 5_000_000, 0.3];
export const LABEL_SCALE_BY_DISTANCE = [1_000, 1.0, 3_000_000, 0.4];
export const LABEL_TRANSLUCENCY_BY_DISTANCE = [1_000, 1.0, 8_000_000, 0.0];

/** Track trail configuration */
export const TRACK_TRAIL_CONFIG = {
  maxPoints: 300,
  width: 2,
  trailOpacity: 0.7,
  decimation: {
    HIGH_ALT_THRESHOLD: 5_000_000,
    HIGH_ALT_STRIDE: 20,
    MID_ALT_THRESHOLD: 500_000,
    MID_ALT_STRIDE: 5,
    LOW_ALT_STRIDE: 1,
  },
};

/** Overlays drawn for the selected satellite. */
export const ORBIT_OVERLAY_CONFIG = {
  samples: 120,          // positions per revolution for the path and ground track
  rebuildMs: 60_000,     // the path is re-sampled this often (and when the elements change)
  pathWidth: 2,
  groundWidth: 1.5,
  pathColor: '#9ca3af',
  groundColor: '#9ca3af',
  groundAlpha: 0.6,
  footprintColor: '#9ca3af',
  footprintAlpha: 0.12,
  footprintOutlineAlpha: 0.5,
};

/** Vessel density discs: the vessel cyan, translucent so they read as counts, not pins. */
export const DENSITY_OVERLAY_CONFIG = {
  fill: 'rgba(6, 182, 212, 0.55)',
  text: '#ffffff',
  /** Camera height a click on a disc flies to: low enough to drop under the feed's cap. */
  zoomHeightM: 2_500_000,
  /** How often a shown grid is re-checked for staleness without a camera move. */
  recheckMs: 15_000,
};

/** The selected aircraft's past path from IntelSky: dashed, in the military green. */
export const FLIGHT_PATH_CONFIG = {
  color: '#22c55e',
  alpha: 0.85,
  width: 2,
  dashLength: 14,
};
