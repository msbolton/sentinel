import { TestBed } from '@angular/core/testing';
import { environment } from '../../../environments/environment';
import { TerrainService } from './terrain.service';

const KEY = 'sentinel.map.terrain';

/** A Terrain that settles when the test says so. */
function makeFakeCesium() {
  const terrains: any[] = [];
  const listeners = () => {
    const l = new Set<(arg?: unknown) => void>();
    return {
      addEventListener: (fn: (arg?: unknown) => void) => {
        l.add(fn);
        return () => l.delete(fn);
      },
      raise: (arg?: unknown) => [...l].forEach((fn) => fn(arg)),
    };
  };
  const Cesium: any = {
    terrains,
    EllipsoidTerrainProvider: jest.fn(function (this: any) {
      this.kind = 'ellipsoid';
    }),
    Terrain: {
      fromWorldTerrain: jest.fn(() => {
        const t: any = { ready: false, readyEvent: listeners(), errorEvent: listeners() };
        t.succeed = () => {
          t.ready = true;
          t.readyEvent.raise({ kind: 'world' });
        };
        t.fail = (err: unknown) => t.errorEvent.raise(err);
        terrains.push(t);
        return t;
      }),
    },
  };
  return Cesium;
}

function makeViewer() {
  return {
    scene: {
      terrainProvider: { kind: 'initial' } as any,
      setTerrain: jest.fn(),
      requestRender: jest.fn(),
    },
  };
}

describe('TerrainService', () => {
  let originalToken: string;

  beforeEach(() => {
    originalToken = environment.cesiumIonAccessToken;
    environment.cesiumIonAccessToken = 'token';
    try {
      localStorage.removeItem(KEY);
    } catch {
      /* ignore */
    }
    jest.spyOn(console, 'warn').mockImplementation(() => undefined);
  });

  afterEach(() => {
    environment.cesiumIonAccessToken = originalToken;
    jest.restoreAllMocks();
    try {
      localStorage.removeItem(KEY);
    } catch {
      /* ignore */
    }
  });

  function service(): TerrainService {
    TestBed.resetTestingModule();
    return TestBed.inject(TerrainService);
  }

  /** init() and let the fake World Terrain load. */
  async function initLoaded(svc: TerrainService, viewer = makeViewer(), Cesium = makeFakeCesium()) {
    const done = svc.init(viewer, Cesium);
    Cesium.terrains[0]?.succeed();
    await done;
    return { viewer, Cesium };
  }

  it('is on by default when an ion token is present, and loads World Terrain', async () => {
    const svc = service();
    const viewer = makeViewer();
    const Cesium = makeFakeCesium();
    const done = svc.init(viewer, Cesium);
    expect(Cesium.Terrain.fromWorldTerrain).toHaveBeenCalledTimes(1);
    expect(viewer.scene.setTerrain).toHaveBeenCalledWith(Cesium.terrains[0]);
    Cesium.terrains[0].succeed();
    await done;
    expect(svc.available()).toBe(true);
    expect(svc.terrainEnabled()).toBe(true);
  });

  it('is off and unavailable without a token, and loads nothing', async () => {
    environment.cesiumIonAccessToken = '';
    const svc = service();
    const viewer = makeViewer();
    const Cesium = makeFakeCesium();
    await svc.init(viewer, Cesium);
    expect(svc.available()).toBe(false);
    expect(svc.terrainEnabled()).toBe(false);
    expect(Cesium.Terrain.fromWorldTerrain).not.toHaveBeenCalled();

    await svc.toggle();
    expect(svc.terrainEnabled()).toBe(false);
    expect(Cesium.Terrain.fromWorldTerrain).not.toHaveBeenCalled();
  });

  it('turning off falls back to the ellipsoid and remembers the choice', async () => {
    const svc = service();
    const { viewer, Cesium } = await initLoaded(svc);

    await svc.toggle();

    expect(svc.terrainEnabled()).toBe(false);
    expect(Cesium.EllipsoidTerrainProvider).toHaveBeenCalledTimes(1);
    expect(viewer.scene.terrainProvider.kind).toBe('ellipsoid');
    expect(localStorage.getItem(KEY)).toBe('false');
    expect(viewer.scene.requestRender).toHaveBeenCalled();
  });

  it('turning back on loads World Terrain again and remembers it', async () => {
    const svc = service();
    const { viewer, Cesium } = await initLoaded(svc);
    await svc.toggle();

    const done = svc.toggle();
    expect(Cesium.Terrain.fromWorldTerrain).toHaveBeenCalledTimes(2);
    expect(viewer.scene.setTerrain).toHaveBeenLastCalledWith(Cesium.terrains[1]);
    Cesium.terrains[1].succeed();
    await done;

    expect(svc.terrainEnabled()).toBe(true);
    expect(localStorage.getItem(KEY)).toBe('true');
  });

  it('starts off when the saved choice is off', async () => {
    localStorage.setItem(KEY, 'false');
    const svc = service();
    const viewer = makeViewer();
    const Cesium = makeFakeCesium();
    await svc.init(viewer, Cesium);
    expect(svc.available()).toBe(true);
    expect(svc.terrainEnabled()).toBe(false);
    expect(Cesium.Terrain.fromWorldTerrain).not.toHaveBeenCalled();
  });

  it('starts on when the saved choice is on', async () => {
    localStorage.setItem(KEY, 'true');
    const svc = service();
    await initLoaded(svc);
    expect(svc.terrainEnabled()).toBe(true);
  });

  it('falls back to the ellipsoid, warns and stays off when World Terrain fails to load', async () => {
    const svc = service();
    const viewer = makeViewer();
    const Cesium = makeFakeCesium();
    const done = svc.init(viewer, Cesium);
    Cesium.terrains[0].fail(new Error('401'));
    await done;

    expect(svc.terrainEnabled()).toBe(false);
    expect(viewer.scene.terrainProvider.kind).toBe('ellipsoid');
    expect(console.warn).toHaveBeenCalled();
    // A failed load is not a choice: it must not overwrite the saved preference.
    expect(localStorage.getItem(KEY)).toBeNull();
  });

  it('stays off when the Cesium terrain call throws', async () => {
    const svc = service();
    const viewer = makeViewer();
    const Cesium = makeFakeCesium();
    Cesium.Terrain.fromWorldTerrain.mockImplementation(() => {
      throw new Error('boom');
    });
    await expect(svc.init(viewer, Cesium)).resolves.toBeUndefined();
    expect(svc.terrainEnabled()).toBe(false);
    expect(console.warn).toHaveBeenCalled();
  });

  it('switching off while a load is in flight cancels it', async () => {
    const svc = service();
    const viewer = makeViewer();
    const Cesium = makeFakeCesium();
    const loading = svc.init(viewer, Cesium);
    await svc.toggle(); // user turns it off before World Terrain arrives
    await loading; // the pending load settles instead of hanging
    expect(svc.terrainEnabled()).toBe(false);
    expect(viewer.scene.terrainProvider.kind).toBe('ellipsoid');
    Cesium.terrains[0].succeed(); // a late arrival changes nothing
    expect(svc.terrainEnabled()).toBe(false);
  });

  it('does not throw when storage throws', async () => {
    jest.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied');
    });
    jest.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied');
    });
    const svc = service();
    const { Cesium } = await initLoaded(svc);
    expect(svc.terrainEnabled()).toBe(true); // default, since nothing could be read
    await expect(svc.toggle()).resolves.toBeUndefined();
    expect(svc.terrainEnabled()).toBe(false);
    expect(Cesium.EllipsoidTerrainProvider).toHaveBeenCalled();
  });

  it('does nothing before init', async () => {
    const svc = service();
    await expect(svc.toggle()).resolves.toBeUndefined();
    expect(svc.terrainEnabled()).toBe(false);
  });
});
