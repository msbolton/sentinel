/**
 * The demo's location blur, the same formula as the gateway's Blur
 * (apps/api-gateway/internal/demo/blur.go): snap to the centre of a 0.1° grid
 * cell, never a random offset. The browser blurs a visitor's fix before it
 * sends it, so the precise position never leaves their machine; the gateway
 * blurs again, which changes nothing.
 */
export const BLUR_CELL_DEG = 0.1;

/** The centre of the northernmost cell, so a blurred latitude stays within [-90, 90]. */
const MAX_LAT = 90 - BLUR_CELL_DEG / 2;

export function blurPosition(lat: number, lon: number): { lat: number; lon: number } {
  let bLat = Math.floor(lat / BLUR_CELL_DEG) * BLUR_CELL_DEG + BLUR_CELL_DEG / 2;
  let bLon = Math.floor(lon / BLUR_CELL_DEG) * BLUR_CELL_DEG + BLUR_CELL_DEG / 2;
  bLat = Math.max(-MAX_LAT, Math.min(MAX_LAT, bLat));
  // Wrap into [-180, 180).
  bLon = (bLon + 180) % 360;
  if (bLon < 0) bLon += 360;
  bLon -= 180;
  return { lat: bLat, lon: bLon };
}
