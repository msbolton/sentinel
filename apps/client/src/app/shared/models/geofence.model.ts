/** A position as the rules API stores it: [longitude, latitude]. */
export type LngLat = [number, number];

export type GeofenceShape = 'polygon' | 'rectangle' | 'circle' | 'ring';

export type GeofenceTrigger = 'ENTRY' | 'EXIT' | 'BOTH';

export type GeofenceSeverity = 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW';

/**
 * The `config` of a GEOFENCE alert rule. `polygon` is present for every
 * shape but `ring`, so the alert service's evaluator works unchanged;
 * `circle` and `ring` keep what the editor needs to reopen the shape.
 */
export interface GeofenceConfig {
  shape: GeofenceShape;
  polygon?: LngLat[];
  triggerOn: GeofenceTrigger;
  /** #rrggbb */
  color: string;
  circle?: { center: LngLat; radiusM: number };
  ring?: { entityId: string; entityName: string; radiusM: number };
}

export interface GeofenceRule {
  id: string;
  name: string;
  ruleType: 'GEOFENCE';
  config: GeofenceConfig;
  /** Empty means every type. */
  monitoredEntityTypes: string[];
  severity: GeofenceSeverity;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

/** A zone that has been drawn but not saved yet. */
export type GeofenceDraft = Omit<GeofenceRule, 'id' | 'createdAt' | 'updatedAt'>;

/** The swatches on the toolbar and in the editor, in order. */
export const GEOFENCE_COLORS: readonly string[] = ['#ef4444', '#f97316', '#f59e0b', '#10b981', '#06b6d4', '#3b82f6', '#8b5cf6'];

/** The colour a rule gets when it was made without one (the seeded zone). */
export const DEFAULT_GEOFENCE_COLOR = '#ef4444';

/** Entity types a zone can watch, with the labels the editor shows. */
export const WATCHABLE_TYPES: { type: string; label: string }[] = [
  { type: 'VESSEL', label: 'Vessels' },
  { type: 'AIRCRAFT', label: 'Aircraft' },
  { type: 'VEHICLE', label: 'Vehicles' },
  { type: 'DRONE', label: 'Drones' },
  { type: 'PERSON', label: 'People' },
];
