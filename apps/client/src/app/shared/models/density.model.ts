/**
 * Live entities counted per type and grid cell inside a box
 * (GET /api/v1/entities/density). The map draws these for views wider than
 * the gateway's detail cap, where it gets no live positions. Cells are
 * [entityType, lat, lon, count]; lat and lon are the entities' mean position.
 */
export interface EntityDensity {
  cellDegrees: number;
  generatedAt: string;
  total: number;
  cells: [string, number, number, number][];
}

/** The gateway's default detail cap, used until connection:ack says otherwise. */
export const DEFAULT_DETAIL_MAX_AREA_SQ_DEG = 2500;
