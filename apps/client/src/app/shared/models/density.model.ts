/**
 * A feed's density grid (`density:grid` on the WebSocket): how many distinct
 * objects it saw per cell while the viewed area was too large to store them
 * one by one. Cells are [latitude, longitude, count] at the cell centre.
 */
export interface DensityGrid {
  feedId: string;
  source: string;
  entityType: string;
  cellDegrees: number;
  windowSeconds: number;
  /** Views larger than this many square degrees get counts instead of objects. */
  capSquareDegrees: number;
  generatedAt: string;
  classification: string;
  total: number;
  cells: [number, number, number][];
}
