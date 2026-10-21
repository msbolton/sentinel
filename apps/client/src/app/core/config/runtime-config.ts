import { environment } from '../../../environments/environment';

type FetchFn = (input: string, init?: RequestInit) => Promise<Response>;

const KEYS = ['googleMapsApiKey', 'cesiumIonAccessToken'] as const;

/**
 * Overlays deployment-time settings onto the build-time environment.
 *
 * The production bundle carries no map keys. The web server writes them to
 * /config.json at container start from its own environment, and this copies them
 * onto `environment` before the app bootstraps, so everything that reads the keys
 * later (Cesium Ion token, 3D buildings) sees the deployed values.
 *
 * A 404 means the deployment has no runtime config (dev server, plain static
 * hosting); any other failure is logged. Neither blocks startup. Empty values
 * never overwrite what the build already holds.
 */
export async function loadRuntimeConfig(fetchFn: FetchFn = (url, init) => fetch(url, init)): Promise<void> {
  try {
    const response = await fetchFn('/config.json', { cache: 'no-store' });
    if (response.status === 404) {
      return;
    }
    if (!response.ok) {
      console.warn(`[Config] /config.json returned HTTP ${response.status}; continuing without runtime config`);
      return;
    }

    const config = await response.json();
    for (const key of KEYS) {
      const value = config?.[key];
      if (typeof value === 'string' && value !== '') {
        environment[key] = value;
      }
    }
  } catch (error) {
    console.warn('[Config] Could not load /config.json; continuing without runtime config:', error);
  }
}
