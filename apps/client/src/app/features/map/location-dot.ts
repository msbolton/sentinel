import { svgToDataUrl } from './cesium-config';

const cache = new Map<string, string>();

function hexToRgb(hex: string): [number, number, number] {
  const clean = hex.replace('#', '').trim();
  if (clean.length === 6) {
    const r = parseInt(clean.slice(0, 2), 16);
    const g = parseInt(clean.slice(2, 4), 16);
    const b = parseInt(clean.slice(4, 6), 16);
    if (!Number.isNaN(r) && !Number.isNaN(g) && !Number.isNaN(b)) {
      return [r, g, b];
    }
  }
  return [59, 130, 246];
}

/**
 * Builds the 64x64 three-ring location-dot SVG (outer halo r=28, inner halo r=18,
 * white-rimmed core r=9) tinted to `hex` (#rrggbb). When `opts.dashed` is true,
 * the outer ring is dashed (used for MOBILE emplacements).
 */
export function locationDotSvg(hex: string, opts?: { dashed?: boolean }): string {
  const [r, g, b] = hexToRgb(hex);
  const dashAttr = opts?.dashed ? ' stroke-dasharray="4 3"' : '';
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64">` +
    `<circle cx="32" cy="32" r="28" fill="rgba(${r},${g},${b},0.12)" stroke="rgba(${r},${g},${b},0.3)" stroke-width="1"${dashAttr}/>` +
    `<circle cx="32" cy="32" r="18" fill="rgba(${r},${g},${b},0.2)" stroke="rgba(${r},${g},${b},0.4)" stroke-width="1"/>` +
    `<circle cx="32" cy="32" r="9" fill="${hex}" stroke="white" stroke-width="2.5"/>` +
    `</svg>`
  );
}

/**
 * Returns a cached SVG data URL for the location dot in `hex` (#rrggbb).
 */
export function locationDotDataUrl(hex: string, opts?: { dashed?: boolean }): string {
  const key = `${hex}:${opts?.dashed ? 'dashed' : 'solid'}`;
  let url = cache.get(key);
  if (!url) {
    url = svgToDataUrl(locationDotSvg(hex, opts));
    cache.set(key, url);
  }
  return url;
}
