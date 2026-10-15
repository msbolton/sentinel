/**
 * Viewport bounds sent to the gateway (`viewport:update`), derived from the
 * Cesium camera's view rectangle.
 */
export interface ViewportBounds {
  north: number;
  south: number;
  east: number;
  west: number;
}

/** The whole globe: what the camera sees when the view rectangle is undefined. */
export const GLOBAL_BOUNDS: Readonly<ViewportBounds> = Object.freeze({
  north: 90,
  south: -90,
  east: 180,
  west: -180,
});

/** A rectangle in radians, as `camera.computeViewRectangle` returns it. */
export interface RadianRectangle {
  north: number;
  south: number;
  east: number;
  west: number;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

/**
 * Convert a camera view rectangle to degree bounds. `undefined` (the camera
 * sees more than the globe) and non-finite values map to the whole globe so
 * the gateway always learns what the client is looking at.
 */
export function rectangleToBounds(
  rect: RadianRectangle | undefined,
  toDegrees: (radians: number) => number,
): ViewportBounds {
  if (!rect) {
    return { ...GLOBAL_BOUNDS };
  }
  const bounds = {
    north: toDegrees(rect.north),
    south: toDegrees(rect.south),
    east: toDegrees(rect.east),
    west: toDegrees(rect.west),
  };
  if (!Object.values(bounds).every(Number.isFinite)) {
    return { ...GLOBAL_BOUNDS };
  }
  return {
    north: clamp(bounds.north, -90, 90),
    south: clamp(bounds.south, -90, 90),
    east: clamp(bounds.east, -180, 180),
    west: clamp(bounds.west, -180, 180),
  };
}

/** True when the bounds cover the whole globe (no point sending a bbox to REST). */
export function isGlobalBounds(b: ViewportBounds): boolean {
  return b.north >= 90 && b.south <= -90 && b.east >= 180 && b.west <= -180;
}
