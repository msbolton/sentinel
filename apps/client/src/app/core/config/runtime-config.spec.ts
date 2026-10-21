import { environment } from '../../../environments/environment';
import { loadRuntimeConfig } from './runtime-config';

function jsonResponse(body: unknown, init: { status?: number } = {}): Response {
  const status = init.status ?? 200;
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response;
}

describe('loadRuntimeConfig', () => {
  const original = { ...environment };
  let fetchMock: jest.Mock;
  let warnSpy: jest.SpyInstance;

  beforeEach(() => {
    fetchMock = jest.fn();
    warnSpy = jest.spyOn(console, 'warn').mockImplementation();
    environment.googleMapsApiKey = '';
    environment.cesiumIonAccessToken = '';
  });

  afterEach(() => {
    Object.assign(environment, original);
    warnSpy.mockRestore();
  });

  it('requests /config.json without caching', async () => {
    fetchMock.mockResolvedValue(jsonResponse({}));

    await loadRuntimeConfig(fetchMock);

    expect(fetchMock).toHaveBeenCalledWith('/config.json', { cache: 'no-store' });
  });

  it('copies both keys onto the environment', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ googleMapsApiKey: 'gmaps-key', cesiumIonAccessToken: 'ion.token' }),
    );

    await loadRuntimeConfig(fetchMock);

    expect(environment.googleMapsApiKey).toBe('gmaps-key');
    expect(environment.cesiumIonAccessToken).toBe('ion.token');
  });

  it('keeps the built-in values for empty or missing keys', async () => {
    environment.googleMapsApiKey = 'built-in';
    fetchMock.mockResolvedValue(jsonResponse({ googleMapsApiKey: '' }));

    await loadRuntimeConfig(fetchMock);

    expect(environment.googleMapsApiKey).toBe('built-in');
    expect(environment.cesiumIonAccessToken).toBe('');
  });

  it('ignores values that are not strings', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ googleMapsApiKey: 42, cesiumIonAccessToken: { a: 1 } }),
    );

    await loadRuntimeConfig(fetchMock);

    expect(environment.googleMapsApiKey).toBe('');
    expect(environment.cesiumIonAccessToken).toBe('');
  });

  it('treats a 404 as no config without warning', async () => {
    fetchMock.mockResolvedValue(jsonResponse(null, { status: 404 }));

    await loadRuntimeConfig(fetchMock);

    expect(environment.googleMapsApiKey).toBe('');
    expect(warnSpy).not.toHaveBeenCalled();
  });

  it('warns and carries on after a server error', async () => {
    fetchMock.mockResolvedValue(jsonResponse(null, { status: 500 }));

    await expect(loadRuntimeConfig(fetchMock)).resolves.toBeUndefined();

    expect(warnSpy).toHaveBeenCalled();
    expect(environment.googleMapsApiKey).toBe('');
  });

  it('warns and carries on when the request fails', async () => {
    fetchMock.mockRejectedValue(new TypeError('network down'));

    await expect(loadRuntimeConfig(fetchMock)).resolves.toBeUndefined();

    expect(warnSpy).toHaveBeenCalled();
  });

  it('warns and carries on when the body is not JSON', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => {
        throw new SyntaxError('Unexpected token <');
      },
    } as unknown as Response);

    await expect(loadRuntimeConfig(fetchMock)).resolves.toBeUndefined();

    expect(warnSpy).toHaveBeenCalled();
    expect(environment.googleMapsApiKey).toBe('');
  });
});
