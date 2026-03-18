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
  [EntityType.VESSEL]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><path fill="white" d="M6 34l4-14h28l4 14H6zm18-20v6m-4-10h8v4h-8z"/><path stroke="white" stroke-width="2" fill="none" d="M24 14v6"/></svg>`,
  [EntityType.SATELLITE]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="64" height="64"><g fill="white"><path d="M321.637,234.88c-22.205,0-44.402,8.47-61.349,25.417c-33.86,33.869-33.86,88.793,0,122.679l122.688-122.679C366.037,243.349,343.832,234.88,321.637,234.88z M273.637,273.646c12.823-12.832,29.867-19.888,48-19.888c11.654,0,22.854,2.914,32.78,8.408l-92.234,92.224C247.991,328.596,251.809,295.464,273.637,273.646z"/><path d="M135.467,55.819l-79.648,79.648l146.068,146.069l79.666-79.666L135.467,55.819z M82.509,135.467l52.958-52.958L254.864,201.87l-52.976,52.976L82.509,135.467z"/><path d="M413.29,68.063L345.219,0L223.11,122.118l68.062,68.071L413.29,68.063z M326.683,129.508l-15.86-15.851l25.944-25.944l15.851,15.842L326.683,129.508z M388.101,68.063l-24.311,24.31l-15.842-15.85l24.302-24.302L388.101,68.063z M345.219,25.216l15.842,15.833L336.767,65.36l-15.841-15.851L345.219,25.216z M309.744,60.699l15.833,15.825l-25.926,25.952L283.8,86.634L309.744,60.699z M248.317,122.118l24.303-24.302l15.841,15.842l-24.31,24.293L248.317,122.118z M299.633,124.83l15.851,15.85l-24.302,24.302l-15.833-15.833L299.633,124.83z"/><path d="M0.009,345.21l68.072,68.063l122.118-122.118l-68.054-68.045L0.009,345.21z M86.652,283.783l15.842,15.842l-25.944,25.952l-15.841-15.851L86.652,283.783z M25.224,345.21l24.293-24.294l15.842,15.833L41.066,361.06L25.224,345.21z M68.081,388.075L52.23,372.242l24.32-24.312l15.834,15.833L68.081,388.075z M103.573,352.6l-15.851-15.851l25.943-25.952l15.842,15.851L103.573,352.6z M164.992,291.172l-24.286,24.294l-15.851-15.842l24.294-24.293L164.992,291.172z M113.665,288.442l-15.85-15.824l24.328-24.32l15.833,15.842L113.665,288.442z"/><path d="M363.422,338.522c-6.864-6.846-18.01-6.854-24.856,0c-6.89,6.864-6.89,18.019-0.017,24.882c6.881,6.872,18.01,6.872,24.873,0C370.293,356.541,370.293,345.412,363.422,338.522z"/><path d="M493.683,378.64c-5.292,27.61-18.536,53.888-39.836,75.18c-21.31,21.31-47.587,34.562-75.172,39.881L382.194,512c31.087-5.976,60.831-20.993,84.835-44.997c24.004-24.004,39.002-53.756,44.962-84.852L493.683,378.64z"/><path d="M466.433,372.277l-18.343-3.344c-3.484,19.089-12.49,37.248-27.181,51.94c-14.701,14.692-32.877,23.714-51.949,27.225l3.361,18.325c22.608-4.124,44.313-14.92,61.77-32.377C451.53,416.608,462.316,394.894,466.433,372.277z"/><path d="M401.143,401.108c10.928-10.928,17.492-24.645,19.748-38.82l-18.396-2.949c-1.702,10.532-6.494,20.538-14.542,28.586v0.008c-8.066,8.049-18.063,12.849-28.586,14.543l2.967,18.396c14.157-2.265,27.866-8.829,38.792-19.757L401.143,401.108z"/></g></svg>`,
  [EntityType.PERSON]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><circle cx="24" cy="14" r="7" fill="white"/><path fill="white" d="M12 38c0-8 5.4-14 12-14s12 6 12 14H12z"/></svg>`,
  [EntityType.FACILITY]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><path fill="white" d="M8 40V16l16-8 16 8v24H8zm8-18v4h4v-4h-4zm12 0v4h4v-4h-4zm-12 8v4h4v-4h-4zm12 0v4h4v-4h-4z"/></svg>`,
  [EntityType.UNKNOWN]: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48"><circle cx="24" cy="24" r="10" fill="white"/></svg>`,
};

/** Orange variant of the aircraft SVG for military (adsb.lol) aircraft */
export const MILITARY_AIRCRAFT_BILLBOARD_SVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 559.185 559.185" width="64" height="64"><path fill="#f97316" stroke="black" stroke-width="12" stroke-linejoin="round" d="M325.303,181.69V81.634C325.303,54.584,303.375,0,276.324,0c-27.056,0-48.984,54.584-48.984,81.634v103.973c-71.341,23.8-186.134,67.442-186.134,101.916c0,9.352,0,16.163,0,21.059c0,3.605,2.864,5.937,6.396,5.202l179.738-37.411v182.131c-43.697,27.332-99.597,66.017-99.597,85.007c0,4.4,0,7.901,0,10.661c0,3.604,2.815,5.747,6.292,4.78l138.036-38.544c3.47-0.974,9.094-0.949,12.571,0.043l133.991,38.451c3.464,0.998,6.272-1.126,6.272-4.736c0-2.767,0-6.261,0-10.661c0-19.395-56.731-59.321-99.597-86.732V273.907l186.281,39.847c3.531,0.753,6.389-1.561,6.389-5.165c0-4.896,0-11.708,0-21.06C517.967,251.067,393.021,204.34,325.303,181.69z"/></svg>`;

/** Convert SVG string to data URL for Cesium billboard */
export function svgToDataUrl(svg: string): string {
  return 'data:image/svg+xml;base64,' + btoa(svg);
}

/** Entity types whose billboard should rotate to match heading */
export const HEADING_ROTATED_TYPES = new Set<string>([
  EntityType.AIRCRAFT,
  EntityType.DRONE,
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
