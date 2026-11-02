import { TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Router } from '@angular/router';
import { EMPTY, Subject, of } from 'rxjs';
import { MapComponent } from './map.component';
import { EntityService } from '../../core/services/entity.service';
import { WebSocketService } from '../../core/services/websocket.service';
import { ThemeService, ThemePreset } from '../../core/services/theme.service';
import { LocationService } from '../../core/services/location.service';
import { BuildingsService } from '../../core/services/buildings.service';
import { TerrainService } from '../../core/services/terrain.service';
import { LayerVisibilityService } from '../../core/services/layer-visibility.service';
import { OrbitOverlayService } from '../../core/services/orbit-overlay.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { GeofenceService } from '../../core/services/geofence.service';
import { FlightPathService } from '../../core/services/flight-path.service';
import { EmplacementService } from '../../core/services/emplacement.service';
import { FeedService } from '../../core/services/feed.service';
import { AuthService } from '../../core/services/auth.service';
import { DemoBounds, DemoPosition, DemoService } from '../../core/services/demo.service';
import { SelectionService } from '../../core/services/selection.service';
import { MapCursorService } from '../../core/services/map-cursor.service';
import {
  Affiliation,
  Classification,
  Entity,
  EntitySource,
  EntityType,
  TrackEnvironment,
} from '../../shared/models/entity.model';
import { Feed } from '../../shared/models/feed.model';
import { ELLIPSE_95 } from './uncertainty';
import { sidcFor, symbolDataUrl } from './milstd2525';
import { ENTITY_TYPE_BILLBOARD_SVGS, svgToDataUrl } from './cesium-config';
import { BearingLayer } from './bearing-layer';
import { locationDotDataUrl } from './location-dot';
import { blurPosition } from '../../shared/blur';
import { ReplayService } from '../../core/services/replay.service';
import { ReplayLayer } from './replay-layer';

// The layer is covered by replay-layer.spec.ts; here only the map's use of it matters.
jest.mock('./replay-layer');

function covFromEllipse(semiMajorM: number, semiMinorM: number, orientationDeg: number) {
  const s1 = semiMajorM / ELLIPSE_95;
  const s2 = semiMinorM / ELLIPSE_95;
  const s1Sq = s1 * s1;
  const s2Sq = s2 * s2;
  const theta = (orientationDeg * Math.PI) / 180;
  const c = Math.cos(theta);
  const s = Math.sin(theta);
  return {
    pnPn: s1Sq * c * c + s2Sq * s * s,
    pnPe: (s1Sq - s2Sq) * s * c,
    pnPu: 0,
    pePe: s1Sq * s * s + s2Sq * c * c,
    pePu: 0,
    puPu: 0,
  };
}

function makeFakeCesium() {
  class FakeColor {
    constructor(
      public red: number,
      public green: number,
      public blue: number,
      public alpha = 1,
    ) {}
    withAlpha(a: number) {
      return new FakeColor(this.red, this.green, this.blue, a);
    }
    static WHITE = new FakeColor(1, 1, 1, 1);
    static BLACK = new FakeColor(0, 0, 0, 1);
    static fromCssColorString(css: string) {
      return new FakeColor(0.5, 0.5, 0.5, 1);
    }
  }
  return {
    Color: FakeColor,
    Cartesian3: {
      fromDegrees: jest.fn((lon: number, lat: number, alt = 0) => ({ lon, lat, alt })),
      UNIT_Z: { x: 0, y: 0, z: 1 },
    },
    Cartesian2: jest.fn(function (this: any, x: number, y: number) {
      this.x = x;
      this.y = y;
    }),
    ConstantPositionProperty: jest.fn(function (this: any, val: any) {
      this.value = val;
      this.setValue = jest.fn((v: any) => {
        this.value = v;
      });
    }),
    EllipseGraphics: jest.fn(function (this: any, opts: any) {
      Object.assign(this, opts);
    }),
    PolylineGraphics: jest.fn(function (this: any, opts: any) {
      Object.assign(this, opts);
    }),
    HeightReference: { NONE: 0, CLAMP_TO_GROUND: 1, RELATIVE_TO_GROUND: 2 },
    VerticalOrigin: { CENTER: 0, BOTTOM: 1 },
    HorizontalOrigin: { LEFT: -1 },
    LabelStyle: { FILL_AND_OUTLINE: 2 },
    Math: {
      toRadians: (deg: number) => (deg * Math.PI) / 180,
    },
  };
}

describe('MapComponent — SIGNAL covariance ellipse & MIL-STD-2525D paths', () => {
  let fixture: ReturnType<typeof TestBed.createComponent<MapComponent>>;
  let component: MapComponent;
  let feedsSignal: ReturnType<typeof signal<Feed[]>>;
  let fakeCesium: ReturnType<typeof makeFakeCesium>;
  let addedEntities: any[];
  let terrainOn: ReturnType<typeof signal<boolean>>;
  let guest: boolean;
  let demo: {
    ownEmplacementId: ReturnType<typeof signal<string | null>>;
    ownPosition: ReturnType<typeof signal<DemoPosition | null>>;
    focusBounds: ReturnType<typeof signal<DemoBounds | null>>;
    welcomeShown: ReturnType<typeof signal<boolean>>;
    reportPosition: jest.Mock;
  };
  let replay: ReturnType<typeof fakeReplayService>;

  function fakeReplayService() {
    const start = new Date('2026-11-02T10:00:00Z');
    const end = new Date('2026-11-02T11:00:00Z');
    return {
      mode: signal<'live' | 'replay'>('live'),
      replay: signal<unknown>(null),
      cursor: signal(start),
      playing: signal(false),
      speed: signal(60),
      clockBounds: signal({ start, end }),
      range: signal<{ start: Date; end: Date } | null>(null),
      seekRequested: signal(0),
      tick: jest.fn(),
    };
  }

  beforeEach(() => {
    feedsSignal = signal<Feed[]>([]);
    fakeCesium = makeFakeCesium();
    addedEntities = [];
    terrainOn = signal(false);
    guest = false;
    replay = fakeReplayService();
    demo = {
      ownEmplacementId: signal<string | null>(null),
      ownPosition: signal<DemoPosition | null>(null),
      focusBounds: signal<DemoBounds | null>(null),
      welcomeShown: signal(true),
      reportPosition: jest.fn(() => Promise.resolve(null)),
    };

    TestBed.configureTestingModule({
      imports: [MapComponent],
      providers: [
        { provide: Router, useValue: { url: '/map', events: EMPTY, navigate: jest.fn(), navigateByUrl: jest.fn() } },
        {
          provide: EntityService,
          useValue: {
            getEntities: jest.fn(() => of({ data: [] })),
            currentEntities$: EMPTY,
            entityEvictions$: EMPTY,
          },
        },
        {
          provide: WebSocketService,
          useValue: {
            entityEvents$: EMPTY,
            ageoutEvents$: EMPTY,
            densityEvents$: EMPTY,
            serverNow: () => Date.now(),
            sendViewportUpdate: jest.fn(),
          },
        },
        { provide: ThemeService, useValue: { activeTheme$: of(ThemePreset.NORMAL) } },
        { provide: LocationService, useValue: { flyTo$: EMPTY } },
        { provide: BuildingsService, useValue: { init: jest.fn(), enabled: signal(false) } },
        { provide: TerrainService, useValue: { init: jest.fn(), toggle: jest.fn(), terrainEnabled: terrainOn.asReadonly(), available: signal(true) } },
        { provide: HttpClient, useValue: { get: jest.fn(() => of([])) } },
        {
          provide: LayerVisibilityService,
          useValue: {
            hidden: signal(new Set<string>()),
            isVisible: jest.fn(() => true),
          },
        },
        {
          provide: OrbitOverlayService,
          useValue: {
            showPath: signal(false),
            showGroundTrack: signal(false),
            showFootprint: signal(false),
          },
        },
        { provide: MapViewportService, useValue: { updateBounds: jest.fn(), set: jest.fn(), bounds: () => null } },
        {
          provide: GeofenceService,
          useValue: {
            startDrawingRequested: new Subject<void>(),
            canWrite: signal(false),
            zones: signal([]),
            selectedId: signal(null),
            draft: signal(null),
            anchors: signal(new Map()),
            load: jest.fn(() => of([])),
          },
        },
        {
          provide: FlightPathService,
          useValue: {
            select: jest.fn(),
            state: signal({ status: 'idle', entityId: null, range: '1h', points: [] }),
          },
        },
        {
          provide: EmplacementService,
          useValue: {
            emplacements: signal([]),
            selectedId: signal(null),
            draft: signal(null),
            showCoverage: signal(true),
            focus: signal(null),
            select: jest.fn(),
            load: jest.fn(() => of([])),
          },
        },
        {
          provide: FeedService,
          useValue: {
            feeds: feedsSignal.asReadonly(),
            loading: signal(false),
            hasLoaded: true,
            load: jest.fn(() => of([])),
          },
        },
        { provide: AuthService, useValue: { isGuest: () => guest } },
        { provide: DemoService, useValue: demo },
        { provide: ReplayService, useValue: replay },
      ],
    });

    TestBed.overrideComponent(MapComponent, {
      set: { imports: [], template: '' },
    });

    jest.spyOn(MapComponent.prototype, 'ngAfterViewInit').mockResolvedValue(undefined);
    fixture = TestBed.createComponent(MapComponent);
    component = fixture.componentInstance;
    (component as any).Cesium = fakeCesium;
    (component as any).viewer = {
      entities: {
        add: jest.fn((opts: any) => {
          const ent = { ...opts };
          addedEntities.push(ent);
          return ent;
        }),
        remove: jest.fn(),
        values: addedEntities,
      },
      scene: {
        requestRender: jest.fn(),
      },
      isDestroyed: jest.fn(() => false),
      destroy: jest.fn(),
    };
  });

  afterEach(() => {
    component.ngOnDestroy();
  });

  function baseSignalEntity(overrides: Partial<Entity> = {}): Entity {
    return {
      id: 'sig-1',
      entityType: EntityType.SIGNAL,
      name: 'S-300 / Tomb Stone',
      source: EntitySource.SIGINT,
      classification: Classification.SECRET,
      position: { latitude: 50.8, longitude: -1.1 },
      metadata: {},
      affiliations: [],
      affiliation: Affiliation.HOSTILE,
      trackEnvironment: TrackEnvironment.GROUND,
      exercise: true,
      feedId: 'ew-1',
      lastFeedId: 'ew-1',
      platformData: {
        rf: {
          frequencyHz: 9.41e9,
          emitterType: 'RADAR_TRACK',
        },
      },
      createdAt: '2026-10-20T12:00:00Z',
      updatedAt: '2026-10-20T12:00:00Z',
      ...overrides,
    };
  }

  it("selects a site's SENSOR entity on a click and stays on the map", () => {
    const actions = new Map<unknown, (e: any) => void>();
    (fakeCesium as any).ScreenSpaceEventHandler = function () {
      return { setInputAction: (fn: (e: any) => void, type: unknown) => actions.set(type, fn) };
    };
    (fakeCesium as any).ScreenSpaceEventType = { LEFT_CLICK: 'L', LEFT_DOUBLE_CLICK: 'D', MOUSE_MOVE: 'M', LEFT_DOWN: 'DN', LEFT_UP: 'UP' };
    (fakeCesium as any).defined = (x: unknown) => x != null;
    const viewer = (component as any).viewer;
    viewer.scene.canvas = document.createElement('canvas');
    viewer.scene.pick = jest.fn(() => ({ id: { _sentinelEmplacement: 'emp-1' } }));
    viewer.selectedEntity = { stale: true };
    const site = baseSignalEntity({ id: 'site-1', entityType: EntityType.SENSOR, identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }] } as Partial<Entity>);
    (component as any).emplacementEntities.set('site-1', site);

    (component as any).setupEventHandlers();
    actions.get('L')!({ position: { x: 1, y: 2 } });

    expect(TestBed.inject(EmplacementService).select).toHaveBeenCalledWith('emp-1');
    expect(component.selectedEntity()).toBe(site);
    expect(viewer.selectedEntity).toBeUndefined();
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
    expect(TestBed.inject(Router).navigateByUrl).not.toHaveBeenCalled();
  });

  it("selects the camera's site entity when a bearing line is clicked", () => {
    const actions = new Map<unknown, (e: any) => void>();
    (fakeCesium as any).ScreenSpaceEventHandler = function () {
      return { setInputAction: (fn: (e: any) => void, type: unknown) => actions.set(type, fn) };
    };
    (fakeCesium as any).ScreenSpaceEventType = { LEFT_CLICK: 'L', LEFT_DOUBLE_CLICK: 'D', MOUSE_MOVE: 'M', LEFT_DOWN: 'DN', LEFT_UP: 'UP' };
    (fakeCesium as any).defined = (x: unknown) => x != null;
    const viewer = (component as any).viewer;
    viewer.scene.canvas = document.createElement('canvas');
    viewer.scene.pick = jest.fn(() => ({ id: { id: 'bearing:site-1:t1', _sentinelBearing: 'site-1', _sentinelBearingKey: 't:t1' } }));
    viewer.selectedEntity = { stale: true };
    const site = baseSignalEntity({ id: 'site-1', entityType: EntityType.SENSOR, identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }] } as Partial<Entity>);
    (component as any).emplacementEntities.set('site-1', site);

    (component as any).setupEventHandlers();
    actions.get('L')!({ position: { x: 1, y: 2 } });

    expect(component.selectedEntity()).toBe(site);
    expect(TestBed.inject(EmplacementService).select).toHaveBeenCalledWith('emp-1');
    expect(viewer.selectedEntity).toBeUndefined();
    expect((component as any).selectedBearingKey()).toBe('t:t1'); // the panel highlights it

    // Any other click forgets it.
    viewer.scene.pick = jest.fn(() => undefined);
    actions.get('L')!({ position: { x: 3, y: 4 } });
    expect((component as any).selectedBearingKey()).toBeNull();
  });

  it('draws the bearing lines a site entity carries and drops them with the site', () => {
    const fields = {
      bearings: JSON.stringify([{ trackId: 't1', azimuthDeg: 90, elevationDeg: 0, lengthM: 1000, reason: 'no_ground_intersection', observedAt: new Date().toISOString() }]),
      bearingTtlSec: '60',
    };
    const site = baseSignalEntity({
      id: 'site-1', entityType: EntityType.SENSOR, feedId: 'cam-1', identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }],
      position: { latitude: 10, longitude: 20, altitude: 5 },
      platformData: { generic: { fields } },
    } as Partial<Entity>);
    (fakeCesium as any).PolylineDashMaterialProperty = jest.fn(function (this: any, o: any) { Object.assign(this, o); });
    const live = new Map<string, any>();
    const viewer = (component as any).viewer;
    viewer.entities.add = jest.fn((cfg: any) => { const e = { ...cfg }; live.set(cfg.id, e); return e; });
    viewer.entities.remove = jest.fn((e: any) => live.delete(e.id));
    (component as any).bearingLayer = new BearingLayer(viewer, fakeCesium);
    (component as any).loadedEmplacementIds = new Set(['emp-1']);

    (component as any).addOrUpdateCesiumEntity(site);
    expect([...live.keys()]).toEqual(['bearing:site-1:t1']);
    expect(live.get('bearing:site-1:t1').polyline.positions[0]).toEqual({ lon: 20, lat: 10, alt: 5 });

    (component as any).removeCesiumEntity('site-1');
    expect(live.size).toBe(0);
  });

  it('clears any previous selection when the clicked site is not on the stream', () => {
    const actions = new Map<unknown, (e: any) => void>();
    (fakeCesium as any).ScreenSpaceEventHandler = function () {
      return { setInputAction: (fn: (e: any) => void, type: unknown) => actions.set(type, fn) };
    };
    (fakeCesium as any).ScreenSpaceEventType = { LEFT_CLICK: 'L', LEFT_DOUBLE_CLICK: 'D', MOUSE_MOVE: 'M', LEFT_DOWN: 'DN', LEFT_UP: 'UP' };
    (fakeCesium as any).defined = (x: unknown) => x != null;
    const viewer = (component as any).viewer;
    viewer.scene.canvas = document.createElement('canvas');
    viewer.scene.pick = jest.fn(() => ({ id: { _sentinelEmplacement: 'emp-2' } }));
    component.selectedEntity.set(baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT }));

    (component as any).setupEventHandlers();
    actions.get('L')!({ position: { x: 1, y: 2 } });

    expect(TestBed.inject(EmplacementService).select).toHaveBeenCalledWith('emp-2');
    expect(component.selectedEntity()).toBeNull();
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
    expect(TestBed.inject(Router).navigateByUrl).not.toHaveBeenCalled();
  });

  describe('keeping the selection live', () => {
    const update = (entity: Entity) =>
      (component as any).handleEntityEvent({ type: 'updated', entity, timestamp: '2026-10-20T12:00:05Z' });

    it('replaces the selected entity with the newer copy from the stream', () => {
      const old = baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT, position: { latitude: 50, longitude: -1 }, lastSeenAt: '2026-10-20T12:00:00Z' } as Partial<Entity>);
      const newer = baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT, position: { latitude: 50.1, longitude: -1 }, lastSeenAt: '2026-10-20T12:00:05Z' } as Partial<Entity>);
      component.selectedEntity.set(old);

      update(newer);

      expect(component.selectedEntity()).toBe(newer);
    });

    it("replaces a selected site's entity, which the emplacement layer draws", () => {
      const site = (lastHeartbeatAt: string) =>
        baseSignalEntity({
          id: 'site-1',
          entityType: EntityType.SENSOR,
          identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }],
          platformData: { generic: { fields: { heartbeat: 'online', lastHeartbeatAt } } },
        } as Partial<Entity>);
      (component as any).loadedEmplacementIds = new Set(['emp-1']);
      const old = site('2026-10-20T12:00:00Z');
      const newer = site('2026-10-20T12:00:05Z');
      (component as any).emplacementEntities.set('site-1', old);
      component.selectedEntity.set(old);

      update(newer);

      expect((component as any).emplacementEntities.get('site-1')).toBe(newer);
      expect(component.selectedEntity()).toBe(newer);
    });

    it('leaves the selection alone when another entity updates', () => {
      const selected = baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT });
      component.selectedEntity.set(selected);

      update(baseSignalEntity({ id: 'ac-2', entityType: EntityType.AIRCRAFT }));

      expect(component.selectedEntity()).toBe(selected);
    });

    it('selects nothing when nothing was selected', () => {
      update(baseSignalEntity({ id: 'ac-2', entityType: EntityType.AIRCRAFT }));

      expect(component.selectedEntity()).toBeNull();
    });
  });

  it('draws a Cesium ellipse from gateway-shaped positionCovariance (pnPn/pnPe/pePe) for SIGNAL only and clears it when covariance clears', () => {
    // Wire payload matching api-gateway internal/stream/bridge.go output
    const wireEntity: Entity = JSON.parse(
      JSON.stringify(
        baseSignalEntity({
          kinematics: {
            positionCovariance: covFromEllipse(1800, 420, 32),
          },
        }),
      ),
    );

    (component as any).addOrUpdateCesiumEntity(wireEntity);
    const cesiumEnt = (component as any).entityMap.get('sig-1');
    expect(cesiumEnt).toBeDefined();
    expect(cesiumEnt.ellipse).toBeDefined();
    expect(cesiumEnt.ellipse.semiMajorAxis).toBeCloseTo(1800, 5);
    expect(cesiumEnt.ellipse.semiMinorAxis).toBeCloseTo(420, 5);
    expect(cesiumEnt.ellipse.rotation).toBeCloseTo(((90 - 32) * Math.PI) / 180, 5);
    expect(cesiumEnt.ellipse.heightReference).toBe(fakeCesium.HeightReference.CLAMP_TO_GROUND);

    // Repeating an update with identical covariance keeps the same EllipseGraphics object
    const firstEllipseRef = cesiumEnt.ellipse;
    (component as any).addOrUpdateCesiumEntity({
      ...wireEntity,
      position: { latitude: 50.81, longitude: -1.09 },
    });
    expect(cesiumEnt.ellipse).toBe(firstEllipseRef);

    // Fix without covariance clears the ellipse
    (component as any).addOrUpdateCesiumEntity({
      ...wireEntity,
      kinematics: {},
    });
    expect(cesiumEnt.ellipse).toBeUndefined();

    // Non-SIGNAL entity with covariance does not draw an ellipse
    const aircraft: Entity = baseSignalEntity({
      id: 'ac-1',
      entityType: EntityType.AIRCRAFT,
      kinematics: {
        positionCovariance: covFromEllipse(1000, 500, 45),
      },
    });
    (component as any).addOrUpdateCesiumEntity(aircraft);
    const acCesium = (component as any).entityMap.get('ac-1');
    expect(acCesium.ellipse).toBeUndefined();
  });

  it('uses 2525D symbols (north-up and untinted WHITE) only for entities from military connections', () => {
    const signalEntity = baseSignalEntity();
    const droneEntity = baseSignalEntity({
      id: 'drn-1',
      entityType: EntityType.DRONE,
      heading: 135,
    });

    // Non-military feed first
    feedsSignal.set([{ id: 'ew-1', config: { military: false } } as unknown as Feed]);
    fixture.detectChanges();

    (component as any).addOrUpdateCesiumEntity(signalEntity);
    (component as any).addOrUpdateCesiumEntity(droneEntity);
    const sigCesium = (component as any).entityMap.get('sig-1');
    const drnCesium = (component as any).entityMap.get('drn-1');
    const expectedSig2525Url = symbolDataUrl(sidcFor(signalEntity));
    const expectedDrn2525Url = symbolDataUrl(sidcFor(droneEntity));
    expect(sigCesium.billboard.image).not.toBe(expectedSig2525Url);
    expect(drnCesium.billboard.image).not.toBe(expectedDrn2525Url);
    expect(drnCesium.billboard.rotation).toBeCloseTo((-135 * Math.PI) / 180, 6);

    // Flip feed to military: true -> effect resyncs billboard in place without touching trails
    const addSpy = jest.spyOn(component as any, 'addOrUpdateCesiumEntity');
    feedsSignal.set([{ id: 'ew-1', config: { military: true } } as unknown as Feed]);
    fixture.detectChanges();

    expect(addSpy).not.toHaveBeenCalled();
    expect(sigCesium.billboard.image).toBe(expectedSig2525Url);
    expect(drnCesium.billboard.image).toBe(expectedDrn2525Url);
    expect(drnCesium.billboard.rotation).toBe(0);
    expect(sigCesium.billboard.color.red).toBe(1);
    expect(sigCesium.billboard.color.green).toBe(1);
    expect(sigCesium.billboard.color.blue).toBe(1);
    expect((component as any).trackTrails.get('sig-1').toArray()).toHaveLength(1);
    expect((component as any).trackTrails.get('drn-1').toArray()).toHaveLength(1);
  });

  it('turns a vehicle billboard with its heading, upright when it has none, and keeps 2525D symbols north-up', () => {
    feedsSignal.set([{ id: 'ew-1', config: { military: false } } as unknown as Feed]);
    fixture.detectChanges();

    const vehicle = baseSignalEntity({ id: 'veh-1', entityType: EntityType.VEHICLE, heading: 90 });
    const parked = baseSignalEntity({ id: 'veh-2', entityType: EntityType.VEHICLE });
    (component as any).addOrUpdateCesiumEntity(vehicle);
    (component as any).addOrUpdateCesiumEntity(parked);
    const vehCesium = (component as any).entityMap.get('veh-1');
    const parkedCesium = (component as any).entityMap.get('veh-2');
    expect(vehCesium.billboard.rotation).toBeCloseTo(-Math.PI / 2, 6);
    expect(parkedCesium.billboard.rotation).toBe(0);
    expect(vehCesium.billboard.image).toBe(svgToDataUrl(ENTITY_TYPE_BILLBOARD_SVGS[EntityType.VEHICLE]));

    feedsSignal.set([{ id: 'ew-1', config: { military: true } } as unknown as Feed]);
    fixture.detectChanges();
    expect(vehCesium.billboard.image).toBe(symbolDataUrl(sidcFor(vehicle)));
    expect(vehCesium.billboard.rotation).toBe(0);
  });

  it('does not resync billboards or push trail points when feed polling returns unchanged military flags', () => {
    feedsSignal.set([{ id: 'ew-1', config: { military: true } } as unknown as Feed]);
    fixture.detectChanges();

    const entity = baseSignalEntity({ heading: 90 });
    (component as any).addOrUpdateCesiumEntity(entity);
    expect((component as any).trackTrails.get('sig-1').toArray()).toHaveLength(1);

    const resyncSpy = jest.spyOn(component as any, 'resyncMilitarySymbols');
    const addSpy = jest.spyOn(component as any, 'addOrUpdateCesiumEntity');

    // Simulate FeedService.poll() setting a fresh array with the same military feed IDs
    feedsSignal.set([
      { id: 'ew-1', config: { military: true } } as unknown as Feed,
      { id: 'opensky', config: {} } as unknown as Feed,
    ]);
    fixture.detectChanges();

    expect(resyncSpy).not.toHaveBeenCalled();
    expect(addSpy).not.toHaveBeenCalled();
    expect((component as any).trackTrails.get('sig-1').toArray()).toHaveLength(1);
  });

  it('updates 2525D affiliation when carried on stream and preserves prior affiliation when omitted', () => {
    feedsSignal.set([{ id: 'ew-1', config: { military: true } } as unknown as Feed]);
    fixture.detectChanges();

    const suspect = baseSignalEntity({ affiliation: Affiliation.SUSPECT });
    (component as any).addOrUpdateCesiumEntity(suspect);

    const suspectUrl = symbolDataUrl(sidcFor(suspect));
    const hostileUrl = symbolDataUrl(sidcFor({ ...suspect, affiliation: Affiliation.HOSTILE }));
    const unknownUrl = symbolDataUrl(sidcFor({ ...suspect, affiliation: Affiliation.UNKNOWN }));
    expect(suspectUrl).not.toBe(hostileUrl);
    expect(hostileUrl).not.toBe(unknownUrl);

    const cesiumEnt = (component as any).entityMap.get('sig-1');
    expect(cesiumEnt.billboard.image).toBe(suspectUrl);

    // 1. Stream update carrying new affiliation ('HOSTILE') updates the 2525D symbol to hostile
    const promoteToHostile: Entity = JSON.parse(
      JSON.stringify({
        ...suspect,
        position: { latitude: 50.81, longitude: -1.09 },
        affiliation: 'HOSTILE',
      }),
    );
    (component as any).handleEntityEvent({
      type: 'updated',
      entity: promoteToHostile,
      timestamp: '2026-10-20T12:00:05Z',
    });
    expect(cesiumEnt.billboard.image).toBe(hostileUrl);

    // 2. Stream update omitting affiliation preserves the stored 'HOSTILE' affiliation instead of reverting to UNKNOWN
    const withoutAffiliation: Entity = JSON.parse(
      JSON.stringify({
        ...suspect,
        position: { latitude: 50.82, longitude: -1.08 },
        affiliation: undefined,
      }),
    );
    (component as any).handleEntityEvent({
      type: 'updated',
      entity: withoutAffiliation,
      timestamp: '2026-10-20T12:00:10Z',
    });
    expect(cesiumEnt.billboard.image).toBe(hostileUrl);
  });
  describe('terrain-aware heights and uncertainty', () => {
    const cov = () => covFromEllipse(300, 120, 20);

    beforeEach(() => terrainOn.set(true));

    function radarDrone(over: Partial<Entity> = {}): Entity {
      return baseSignalEntity({
        id: 'rdr-1',
        entityType: EntityType.DRONE,
        name: 'Radar drone',
        trackEnvironment: TrackEnvironment.AIR,
        position: { latitude: 46.5, longitude: 8.0, altitude: 940 },
        platformData: { radar: { heightAboveGroundM: 120 } },
        ...over,
      });
    }

    it('adds a radar drone relative to the ground at its height above ground', () => {
      (component as any).addOrUpdateCesiumEntity(radarDrone());
      const ent = (component as any).entityMap.get('rdr-1');
      expect(ent.billboard.heightReference).toBe(fakeCesium.HeightReference.RELATIVE_TO_GROUND);
      expect(ent.label.heightReference).toBe(fakeCesium.HeightReference.RELATIVE_TO_GROUND);
      expect(ent._posProp.value).toEqual({ lon: 8.0, lat: 46.5, alt: 120 });
    });

    it('draws a radar drone absolute at its altitude while terrain is off', () => {
      terrainOn.set(false);
      (component as any).addOrUpdateCesiumEntity(radarDrone());
      const ent = (component as any).entityMap.get('rdr-1');
      expect(ent.billboard.heightReference).toBe(fakeCesium.HeightReference.NONE);
      expect(ent._posProp.value).toEqual({ lon: 8.0, lat: 46.5, alt: 940 });
    });

    it('switches an existing radar drone between absolute and relative when terrain toggles', () => {
      (component as any).addOrUpdateCesiumEntity(radarDrone());
      const ent = (component as any).entityMap.get('rdr-1');
      expect(ent.billboard.heightReference).toBe(fakeCesium.HeightReference.RELATIVE_TO_GROUND);

      terrainOn.set(false);
      fixture.detectChanges();
      expect(ent.billboard.heightReference).toBe(fakeCesium.HeightReference.NONE);
      expect(ent.label.heightReference).toBe(fakeCesium.HeightReference.NONE);
      expect(ent._posProp.value).toEqual({ lon: 8.0, lat: 46.5, alt: 940 });

      terrainOn.set(true);
      fixture.detectChanges();
      expect(ent.billboard.heightReference).toBe(fakeCesium.HeightReference.RELATIVE_TO_GROUND);
      expect(ent.label.heightReference).toBe(fakeCesium.HeightReference.RELATIVE_TO_GROUND);
      expect(ent._posProp.value).toEqual({ lon: 8.0, lat: 46.5, alt: 120 });
    });

    it('keeps a moving radar drone at its height above ground, not its ellipsoid altitude', () => {
      (component as any).addOrUpdateCesiumEntity(
        radarDrone({ lastSeenAt: new Date().toISOString(), kinematics: { velocity: { north: 25, east: 0, up: 0 } } }),
      );
      expect((component as any).deadReckoning.positionOf('rdr-1', Date.now())).not.toBeNull(); // it is dead-reckoned
      const ent = (component as any).entityMap.get('rdr-1');
      expect(ent._posProp.value.alt).toBe(120);
    });

    it('adds a GROUND vehicle clamped to the ground', () => {
      (component as any).addOrUpdateCesiumEntity(
        baseSignalEntity({
          id: 'veh-1',
          entityType: EntityType.VEHICLE,
          trackEnvironment: TrackEnvironment.GROUND,
          position: { latitude: 46.5, longitude: 8.0, altitude: 610 },
          platformData: { eoir: { band: 'LWIR' } },
        }),
      );
      const ent = (component as any).entityMap.get('veh-1');
      expect(ent.billboard.heightReference).toBe(fakeCesium.HeightReference.CLAMP_TO_GROUND);
    });

    it('adds an airborne entity with an altitude at that absolute height', () => {
      (component as any).addOrUpdateCesiumEntity(
        baseSignalEntity({
          id: 'ac-9',
          entityType: EntityType.AIRCRAFT,
          trackEnvironment: TrackEnvironment.AIR,
          position: { latitude: 46.5, longitude: 8.0, altitude: 9000 },
        }),
      );
      const ent = (component as any).entityMap.get('ac-9');
      expect(ent.billboard.heightReference).toBe(fakeCesium.HeightReference.NONE);
      expect(ent._posProp.value.alt).toBe(9000);
    });

    it('draws uncertainty ellipses for radar and EO/IR entities, not for a plain vehicle', () => {
      (component as any).addOrUpdateCesiumEntity(radarDrone({ kinematics: { positionCovariance: cov() } }));
      (component as any).addOrUpdateCesiumEntity(
        baseSignalEntity({
          id: 'eo-1',
          entityType: EntityType.VEHICLE,
          position: { latitude: 46.5, longitude: 8.0 },
          platformData: { eoir: { band: 'MWIR' } },
          kinematics: { positionCovariance: cov() },
        }),
      );
      (component as any).addOrUpdateCesiumEntity(
        baseSignalEntity({
          id: 'veh-2',
          entityType: EntityType.VEHICLE,
          position: { latitude: 46.5, longitude: 8.0 },
          platformData: {},
          kinematics: { positionCovariance: cov() },
        }),
      );
      const map = (component as any).entityMap;
      expect(map.get('rdr-1').ellipse.semiMajorAxis).toBeCloseTo(300, 5);
      expect(map.get('rdr-1').ellipse.heightReference).toBe(fakeCesium.HeightReference.CLAMP_TO_GROUND);
      expect(map.get('eo-1').ellipse.semiMajorAxis).toBeCloseTo(300, 5);
      expect(map.get('veh-2').ellipse).toBeUndefined();
    });

    it('outlines each uncertainty ellipse with a ground-clamped polyline, terrain on or off, and removes it with the ellipse', () => {
      const outlineOf = (ent: any) => addedEntities.filter((e) => e.parent === ent && e.polyline);
      terrainOn.set(false);
      (component as any).addOrUpdateCesiumEntity(baseSignalEntity({ kinematics: { positionCovariance: cov() } }));
      const ent = (component as any).entityMap.get('sig-1');
      expect(ent.ellipse.outline).toBe(false); // Cesium drops it when clamped: the polyline is the outline
      let lines = outlineOf(ent);
      expect(lines).toHaveLength(1);
      expect(lines[0].polyline.clampToGround).toBe(true);
      expect(lines[0].polyline.positions.length).toBeGreaterThan(30);
      const first = lines[0].polyline.positions[0];
      const last = lines[0].polyline.positions[lines[0].polyline.positions.length - 1];
      expect(first).toEqual(last); // a closed ring

      terrainOn.set(true);
      fixture.detectChanges();
      expect(ent.ellipse.outline).toBe(false);
      expect(ent.ellipse.semiMajorAxis).toBeCloseTo(300, 5);
      lines = outlineOf(ent);
      expect(lines.length).toBeGreaterThan(0);
      expect(lines[lines.length - 1].polyline.clampToGround).toBe(true);

      // The covariance clears: the outline goes with the ellipse.
      const remove = (component as any).viewer.entities.remove as jest.Mock;
      remove.mockClear();
      (component as any).addOrUpdateCesiumEntity(baseSignalEntity({ kinematics: {} }));
      expect(ent.ellipse).toBeUndefined();
      expect(remove).toHaveBeenCalledWith(lines[lines.length - 1]);
    });

    it('draws a grazing-angle sliver at least 15 m wide, with a visible fill', () => {
      (component as any).addOrUpdateCesiumEntity(
        baseSignalEntity({ id: 'eo-2', platformData: { eoir: { band: 'LWIR' } }, kinematics: { positionCovariance: covFromEllipse(1100, 2.4, 30) } }),
      );
      const ent = (component as any).entityMap.get('eo-2');
      expect(ent.ellipse.semiMajorAxis).toBeCloseTo(1100, 5);
      expect(ent.ellipse.semiMinorAxis).toBe(15);
      expect(ent.ellipse.material.alpha).toBeGreaterThanOrEqual(0.2);
    });

    it('removes the outline when the entity goes', () => {
      (component as any).addOrUpdateCesiumEntity(baseSignalEntity({ kinematics: { positionCovariance: cov() } }));
      const ent = (component as any).entityMap.get('sig-1');
      const line = addedEntities.find((e) => e.parent === ent && e.polyline);
      expect(line).toBeDefined();
      (component as any).removeCesiumEntity('sig-1');
      expect((component as any).viewer.entities.remove).toHaveBeenCalledWith(line);
    });
  });

  describe('as a demo guest', () => {
    let camera: { setView: jest.Mock; flyTo: jest.Mock; positionCartographic: any; heading: number; pitch: number };
    const savedCamera = JSON.stringify({ lon: 1, lat: 2, alt: 3, heading: 0, pitch: 0 });

    function geolocation(result: { lat: number; lon: number } | 'denied'): void {
      Object.defineProperty(navigator, 'geolocation', {
        configurable: true,
        value: {
          getCurrentPosition: jest.fn((ok: (p: any) => void, fail: (e: any) => void) =>
            result === 'denied' ? fail({ code: 1 }) : ok({ coords: { latitude: result.lat, longitude: result.lon } }),
          ),
        },
      });
    }

    function site(id: string, emplacementId: string): Entity {
      return baseSignalEntity({ id, entityType: EntityType.SENSOR, identities: [{ type: 'EMPLACEMENT', value: emplacementId }] } as Partial<Entity>);
    }

    beforeEach(() => {
      guest = true;
      camera = { setView: jest.fn(), flyTo: jest.fn(), positionCartographic: { longitude: 0.1, latitude: 0.2, height: 9 }, heading: 0, pitch: 0 };
      (component as any).viewer.camera = camera;
      (fakeCesium as any).Math.toDegrees = (rad: number) => (rad * 180) / Math.PI;
      (fakeCesium as any).Rectangle = { fromDegrees: jest.fn((west: number, south: number, east: number, north: number) => ({ west, south, east, north })) };
      localStorage.setItem('sentinel-camera-position', savedCamera);
    });

    afterEach(() => {
      delete (navigator as any).geolocation;
      localStorage.clear();
    });

    it('opens at its own blurred cell, not the saved camera, and reports that fix once', () => {
      geolocation({ lat: 51.5, lon: -0.12 });

      (component as any).initUserLocation();

      const cell = blurPosition(51.5, -0.12);
      expect(camera.setView).not.toHaveBeenCalled();
      expect(demo.reportPosition).toHaveBeenCalledTimes(1);
      expect(demo.reportPosition).toHaveBeenCalledWith({ lat: 51.5, lon: -0.12 });
      expect(camera.flyTo).toHaveBeenCalledWith(expect.objectContaining({ destination: { lon: cell.lon, lat: cell.lat, alt: 50000 } }));
      expect(camera.flyTo).not.toHaveBeenCalledWith(expect.objectContaining({ destination: { lon: -0.12, lat: 51.5, alt: 50000 } }));
      expect(localStorage.getItem('sentinel-has-geolocated')).toBeNull();
    });

    it('flies a placed guest to their blurred cell when the map opens again and on "my location"', () => {
      demo.ownPosition.set({ lat: 51.5, lon: -0.12, source: 'browser' });
      const cell = blurPosition(51.5, -0.12);
      const toCell = expect.objectContaining({ destination: { lon: cell.lon, lat: cell.lat, alt: 50000 } });

      (component as any).initUserLocation();
      expect(demo.reportPosition).not.toHaveBeenCalled();
      expect(camera.flyTo).toHaveBeenLastCalledWith(toCell);

      camera.flyTo.mockClear();
      component.goToMyLocation();
      expect(camera.flyTo).toHaveBeenCalledTimes(1);
      expect(camera.flyTo).toHaveBeenCalledWith(toCell);
    });

    it("flies to the gateway's answer when the visitor declines, and stays put when there is none", async () => {
      geolocation('denied');
      demo.reportPosition.mockResolvedValueOnce({ lat: 48.85, lon: 2.35, source: 'ip' });

      (component as any).initUserLocation();
      await Promise.resolve();

      const cell = blurPosition(48.85, 2.35);
      expect(demo.reportPosition).toHaveBeenCalledWith('denied');
      expect(camera.flyTo).toHaveBeenCalledWith(expect.objectContaining({ destination: { lon: cell.lon, lat: cell.lat, alt: 50000 } }));

      camera.flyTo.mockClear();
      demo.reportPosition.mockResolvedValueOnce(null);
      (component as any).initUserLocation();
      await Promise.resolve();
      expect(camera.flyTo).not.toHaveBeenCalled();
    });

    it('asks for the location with a 10 s timeout, accepting a fix up to a minute old', () => {
      geolocation({ lat: 51.5, lon: -0.12 });
      (component as any).initUserLocation();
      expect((navigator.geolocation.getCurrentPosition as jest.Mock).mock.calls[0][2]).toEqual({ timeout: 10_000, maximumAge: 60_000 });
    });

    it('falls back to the IP location when the prompt goes unanswered for 10 s, then refines if the browser answers later', async () => {
      jest.useFakeTimers();
      try {
        let answer!: (p: any) => void;
        Object.defineProperty(navigator, 'geolocation', {
          configurable: true,
          value: { getCurrentPosition: jest.fn((ok: (p: any) => void) => (answer = ok)) },
        });
        demo.reportPosition.mockResolvedValueOnce({ lat: 48.85, lon: 2.35, source: 'ip' });

        (component as any).initUserLocation();
        jest.advanceTimersByTime(9_999);
        expect(demo.reportPosition).not.toHaveBeenCalled();
        jest.advanceTimersByTime(1);
        expect(demo.reportPosition).toHaveBeenCalledTimes(1);
        expect(demo.reportPosition).toHaveBeenCalledWith('denied');

        answer({ coords: { latitude: 51.5, longitude: -0.12 } });
        expect(demo.reportPosition).toHaveBeenCalledTimes(2);
        expect(demo.reportPosition).toHaveBeenLastCalledWith({ lat: 51.5, lon: -0.12 });
      } finally {
        jest.useRealTimers();
      }
    });

    it('a refusal after the fallback, or a timeout error, does not report twice', () => {
      jest.useFakeTimers();
      try {
        let fail!: (e: any) => void;
        Object.defineProperty(navigator, 'geolocation', {
          configurable: true,
          value: { getCurrentPosition: jest.fn((_ok: unknown, no: (e: any) => void) => (fail = no)) },
        });
        (component as any).initUserLocation();
        jest.advanceTimersByTime(10_000);
        fail({ code: 3 });
        expect(demo.reportPosition).toHaveBeenCalledTimes(1);
        expect(demo.reportPosition).toHaveBeenCalledWith('denied');
        jest.advanceTimersByTime(60_000);
        expect(demo.reportPosition).toHaveBeenCalledTimes(1);
      } finally {
        jest.useRealTimers();
      }
    });

    it('a quick refusal reports once, at once, with no fallback later', () => {
      jest.useFakeTimers();
      try {
        geolocation('denied');
        (component as any).initUserLocation();
        expect(demo.reportPosition).toHaveBeenCalledTimes(1);
        jest.advanceTimersByTime(20_000);
        expect(demo.reportPosition).toHaveBeenCalledTimes(1);
      } finally {
        jest.useRealTimers();
      }
    });

    it('waits for the welcome card to be on screen before asking for the location, then asks once', () => {
      demo.welcomeShown.set(false);
      geolocation({ lat: 51.5, lon: -0.12 });

      (component as any).initUserLocation();
      fixture.detectChanges();
      expect(navigator.geolocation.getCurrentPosition).not.toHaveBeenCalled();
      expect(demo.reportPosition).not.toHaveBeenCalled();

      demo.welcomeShown.set(true);
      fixture.detectChanges();
      expect(demo.reportPosition).toHaveBeenCalledTimes(1);
      expect(demo.reportPosition).toHaveBeenCalledWith({ lat: 51.5, lon: -0.12 });

      // The signal flipping again (a new session) does not ask a second time.
      demo.welcomeShown.set(false);
      fixture.detectChanges();
      demo.welcomeShown.set(true);
      fixture.detectChanges();
      expect(demo.reportPosition).toHaveBeenCalledTimes(1);
    });

    it('never saves its camera', () => {
      localStorage.clear();
      (component as any).saveCameraPosition();
      expect(localStorage.getItem('sentinel-camera-position')).toBeNull();
    });

    it('draws its own marker at ownPosition as a larger bright violet dot', () => {
      demo.ownPosition.set({ lat: 51.5, lon: -0.12, source: 'browser' });
      fixture.detectChanges();

      const marker = addedEntities.find((e) => e.billboard?.image === locationDotDataUrl('#a78bfa'));
      expect(marker).toBeDefined();
      expect(marker.position).toEqual({ lon: -0.12, lat: 51.5, alt: 0 });
      expect(marker.billboard.scale).toBeGreaterThan(0.7);
      expect(addedEntities.some((e) => e.billboard?.image === locationDotDataUrl('#3b82f6'))).toBe(false);
    });

    it('leaves its own site out of the emplacement layer, also when its id arrives later', () => {
      const layer = { sync: jest.fn(), destroy: jest.fn() };
      (component as any).emplacementLayer = layer;
      (TestBed.inject(EmplacementService).emplacements as any).set([
        { id: 'own', active: true, feedId: 'visitors' },
        { id: 'other', active: true, feedId: 'visitors' },
      ]);
      fixture.detectChanges();
      expect(layer.sync.mock.calls.at(-1)[0].map((e: any) => e.id)).toEqual(['own', 'other']);

      demo.ownEmplacementId.set('own');
      fixture.detectChanges();
      expect(layer.sync.mock.calls.at(-1)[0].map((e: any) => e.id)).toEqual(['other']);
    });

    it("skips its own site's entity on the stream, removing one drawn before its id arrived", () => {
      const event = (entity: Entity) => (component as any).handleEntityEvent({ type: 'updated', entity, timestamp: '2026-10-20T12:00:05Z' });
      const entityMap = (component as any).entityMap as Map<string, unknown>;
      event(site('own-site', 'own'));
      event(site('other-site', 'other'));
      expect([...entityMap.keys()]).toEqual(['own-site', 'other-site']);

      demo.ownEmplacementId.set('own');
      fixture.detectChanges();
      expect([...entityMap.keys()]).toEqual(['other-site']);

      event(site('own-site', 'own'));
      expect([...entityMap.keys()]).toEqual(['other-site']);
      expect((component as any).emplacementEntities.has('own-site')).toBe(false);
    });

    it('flies to focusBounds when they are set, then clears them', () => {
      demo.focusBounds.set({ west: 1, south: 2, east: 3, north: 4 });
      fixture.detectChanges();

      expect(camera.flyTo).toHaveBeenCalledWith(expect.objectContaining({ destination: { west: 1, south: 2, east: 3, north: 4 } }));
      expect(demo.focusBounds()).toBeNull();
    });

    it('a non-guest keeps the saved camera, its blue dot and its own sites', () => {
      guest = false;
      geolocation({ lat: 51.5, lon: -0.12 });

      (component as any).initUserLocation();

      expect(camera.setView).toHaveBeenCalled();
      expect(camera.flyTo).not.toHaveBeenCalled();
      expect(demo.reportPosition).not.toHaveBeenCalled();
      expect(addedEntities.some((e) => e.billboard?.image === locationDotDataUrl('#3b82f6'))).toBe(true);
      localStorage.clear();
      (component as any).saveCameraPosition();
      expect(localStorage.getItem('sentinel-camera-position')).not.toBeNull();
    });
  });

  describe('publishing the cursor and camera altitude', () => {
    function moveHandler() {
      const actions = new Map<unknown, (e: any) => void>();
      (fakeCesium as any).ScreenSpaceEventHandler = function () {
        return { setInputAction: (fn: (e: any) => void, type: unknown) => actions.set(type, fn) };
      };
      (fakeCesium as any).ScreenSpaceEventType = { LEFT_CLICK: 'L', LEFT_DOUBLE_CLICK: 'D', MOUSE_MOVE: 'M', LEFT_DOWN: 'DN', LEFT_UP: 'UP' };
      (component as any).viewer.scene.canvas = document.createElement('canvas');
      (component as any).setupEventHandlers();
      return actions.get('M')!;
    }

    it('publishes the globe pick under the mouse as lat/lon, and null off the globe', () => {
      const cursor = TestBed.inject(MapCursorService);
      const pick = jest.spyOn(component as any, 'pickLngLat').mockReturnValue([-1.1, 50.8]);
      const move = moveHandler();

      move({ endPosition: { x: 1, y: 2 } });
      expect(pick).toHaveBeenCalledWith({ x: 1, y: 2 });
      expect(cursor.cursor()).toEqual({ lat: 50.8, lon: -1.1 });

      pick.mockReturnValue(null);
      move({ endPosition: { x: 3, y: 4 } });
      expect(cursor.cursor()).toBeNull();
    });

    it('clears the cursor when the pointer leaves the map canvas, and stops listening once destroyed', () => {
      const cursor = TestBed.inject(MapCursorService);
      jest.spyOn(component as any, 'pickLngLat').mockReturnValue([20, 10]);
      const move = moveHandler();
      const canvas: HTMLCanvasElement = (component as any).viewer.scene.canvas;

      move({ endPosition: { x: 1, y: 2 } });
      expect(cursor.cursor()).toEqual({ lat: 10, lon: 20 });
      canvas.dispatchEvent(new MouseEvent('mouseleave'));
      expect(cursor.cursor()).toBeNull();

      component.ngOnDestroy();
      cursor.setCursor({ lat: 1, lon: 2 });
      canvas.dispatchEvent(new MouseEvent('mouseleave'));
      expect(cursor.cursor()).toEqual({ lat: 1, lon: 2 });
    });

    it('keeps publishing mid-drag and picks once per move', () => {
      const cursor = TestBed.inject(MapCursorService);
      const pick = jest.spyOn(component as any, 'pickLngLat').mockReturnValue([20, 10]);
      const moveHandle = jest.fn(() => ({ kind: 'circle' }));
      (component as any).geofenceLayer = { moveHandle, destroy: jest.fn() };
      (component as any).handleDrag = { zoneId: 'z1', index: 0, config: null };
      const move = moveHandler();

      move({ endPosition: { x: 1, y: 2 } });

      expect(pick).toHaveBeenCalledTimes(1);
      expect(moveHandle).toHaveBeenCalledWith('z1', 0, [20, 10]);
      expect(cursor.cursor()).toEqual({ lat: 10, lon: 20 });
    });

    it('publishes the camera height once up front and after each camera move', () => {
      const cursor = TestBed.inject(MapCursorService);
      const camera = { positionCartographic: { height: 1500 }, moveEnd: { addEventListener: jest.fn() } };
      (component as any).viewer.camera = camera;
      jest.spyOn(component as any, 'saveCameraPosition').mockImplementation(() => undefined);
      (component as any).setupCameraMoveHandler();
      (component as any).publishCameraAltitude();
      expect(cursor.cameraAltitudeM()).toBe(1500);

      camera.positionCartographic.height = 42_000;
      camera.moveEnd.addEventListener.mock.calls[0][0]();
      expect(cursor.cameraAltitudeM()).toBe(42_000);
    });
  });

  describe('shared selection', () => {
    it('is the SelectionService signal itself', () => {
      expect(component.selectedEntity).toBe(TestBed.inject(SelectionService).entity);
    });

    it('flies the camera 60 km above a track selected through the service', () => {
      const flyTo = jest.fn();
      (component as any).viewer.camera = { flyTo };
      (component as any).subscribeToFlyTo();

      TestBed.inject(SelectionService).select(baseSignalEntity({ position: { latitude: 12, longitude: 44 } }));

      expect(flyTo).toHaveBeenCalledWith({ destination: { lon: 44, lat: 12, alt: 60000 }, duration: 1.2 });
    });

    function stubScreenTransforms(visible = true) {
      const toWindow = jest.fn((_scene: unknown, _world: unknown) => ({ x: 100, y: 200 }) as { x: number; y: number } | undefined);
      const isPointVisible = jest.fn(() => visible);
      (fakeCesium as any).SceneTransforms = { worldToWindowCoordinates: toWindow };
      (fakeCesium as any).Ellipsoid = { WGS84: {} };
      (fakeCesium as any).EllipsoidalOccluder = jest.fn(() => ({ isPointVisible }));
      const viewer = (component as any).viewer;
      viewer.scene.camera = { positionWC: { x: 0, y: 0, z: 1e7 } };
      viewer.clock = { currentTime: 'now' };
      return { toWindow, isPointVisible };
    }
    const update = () => (component as any).updateSelectionScreenPoint();

    it('reports the selected track in window pixels after each render, setting only changes', () => {
      const selection = TestBed.inject(SelectionService);
      const { toWindow } = stubScreenTransforms();

      update();
      expect(selection.screenPoint()).toBeNull();
      expect(toWindow).not.toHaveBeenCalled();

      component.selectedEntity.set(baseSignalEntity({ position: { latitude: 50, longitude: -1, altitude: 300 } }));
      update();
      expect(toWindow).toHaveBeenCalledWith((component as any).viewer.scene, { lon: -1, lat: 50, alt: 300 });
      const first = selection.screenPoint();
      expect(first).toEqual({ x: 100, y: 200 });

      update();
      expect(selection.screenPoint()).toBe(first);

      toWindow.mockReturnValue(undefined);
      update();
      expect(selection.screenPoint()).toBeNull();

      toWindow.mockReturnValue({ x: 1, y: 2 });
      component.selectedEntity.set(baseSignalEntity({ position: undefined }));
      update();
      expect(selection.screenPoint()).toBeNull();
    });

    it('places the point where the track is drawn, not at its last fix', () => {
      const { toWindow } = stubScreenTransforms();
      const drawn = { x: 1, y: 2, z: 3 };
      const getValue = jest.fn(() => drawn);
      (component as any).entityMap.set('sig-1', { position: { getValue } });
      component.selectedEntity.set(baseSignalEntity({ position: { latitude: 50, longitude: -1 } }));

      update();

      expect(getValue).toHaveBeenCalledWith('now');
      expect(toWindow).toHaveBeenCalledWith((component as any).viewer.scene, drawn);
      expect(TestBed.inject(SelectionService).screenPoint()).toEqual({ x: 100, y: 200 });
    });

    it('reports no point for a track hidden behind the globe', () => {
      const { toWindow, isPointVisible } = stubScreenTransforms(false);
      component.selectedEntity.set(baseSignalEntity({ position: { latitude: -50, longitude: 179 } }));

      update();

      expect(isPointVisible).toHaveBeenCalledWith({ lon: 179, lat: -50, alt: 0 });
      expect(toWindow).not.toHaveBeenCalled();
      expect(TestBed.inject(SelectionService).screenPoint()).toBeNull();
    });

    it('clears the shared selection when the map is destroyed', () => {
      const selection = TestBed.inject(SelectionService);
      component.selectedEntity.set(baseSignalEntity());
      selection.screenPoint.set({ x: 1, y: 2 });

      component.ngOnDestroy();

      expect(selection.entity()).toBeNull();
      expect(selection.screenPoint()).toBeNull();
    });

    it('leaves the shared selection alone when a map that never made a viewer is destroyed', () => {
      const selection = TestBed.inject(SelectionService);
      const picked = baseSignalEntity();
      selection.entity.set(picked);
      selection.screenPoint.set({ x: 1, y: 2 });
      (component as any).viewer = null;

      component.ngOnDestroy();

      expect(selection.entity()).toBe(picked);
      expect(selection.screenPoint()).toEqual({ x: 1, y: 2 });
    });
  });

  describe('replay mode', () => {
    let viewer: any;
    let clock: any;
    const at = (iso: string) => new Date(iso);

    beforeEach(() => {
      Object.assign(fakeCesium as any, {
        JulianDate: {
          fromDate: (d: Date) => ({ d }),
          toDate: (j: { d: Date }) => j.d,
          now: () => ({ d: 'now' }),
        },
        ClockRange: { UNBOUNDED: 0, CLAMPED: 1, LOOP_STOP: 2 },
        Rectangle: jest.fn(),
        Math: { ...fakeCesium.Math, toDegrees: (r: number) => (r * 180) / Math.PI },
      });
      clock = {
        startTime: null,
        stopTime: null,
        currentTime: null,
        clockRange: 0,
        multiplier: 1,
        shouldAnimate: false,
        onTick: { addEventListener: jest.fn() },
      };
      viewer = (component as any).viewer;
      viewer.clock = clock;
      viewer.dataSources = { add: jest.fn(), remove: jest.fn() };
      viewer.camera = { computeViewRectangle: jest.fn(() => undefined) };
      viewer.scene.globe = { ellipsoid: {} };
      (component as any).clusterSources = new Map(
        [EntityType.AIRCRAFT, EntityType.VESSEL].map((t) => [
          t,
          { show: true, entities: { add: jest.fn((o: any) => ({ ...o })), remove: jest.fn(), suspendEvents: jest.fn(), resumeEvents: jest.fn() } },
        ]),
      );
      (component as any).densitySource = { show: true, entities: {} };
      (ReplayLayer as jest.MockedClass<typeof ReplayLayer>).mockClear();
    });

    const layer = () => (component as any).replayLayer as jest.Mocked<ReplayLayer>;
    const sources = () => [...(component as any).clusterSources.values()];
    function enter() {
      replay.mode.set('replay');
      fixture.detectChanges();
    }

    it('hides the live layers, stops the frame loop and drops live entity events', () => {
      const cancel = jest.spyOn(window, 'cancelAnimationFrame');
      (component as any).drFrame = 42;
      const live = baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT });
      (component as any).addOrUpdateCesiumEntity(live);
      expect((component as any).entityMap.has('ac-1')).toBe(true);

      enter();

      expect(ReplayLayer).toHaveBeenCalledTimes(1);
      expect(sources().every((s) => s.show === false)).toBe(true);
      expect((component as any).densitySource.show).toBe(false);
      expect(cancel).toHaveBeenCalledWith(42);
      expect((component as any).drFrame).toBeNull();
      expect((component as any).entityMap.size).toBe(0);

      (component as any).processCesiumBatch([
        { type: 'created', entity: baseSignalEntity({ id: 'ac-2', entityType: EntityType.AIRCRAFT }), timestamp: '2026-11-02T10:00:00Z' },
      ]);
      expect((component as any).entityMap.size).toBe(0);
    });

    it('draws each replay response on the replay layer', () => {
      enter();
      const response = { entities: [], startTime: 'a', endTime: 'b', truncated: false };
      replay.replay.set(response);
      fixture.detectChanges();

      expect(layer().build).toHaveBeenCalledWith(response);
    });

    it('still reports the viewport but fetches no live entities while replaying', () => {
      enter();
      const entities = TestBed.inject(EntityService);
      (entities.getEntities as jest.Mock).mockClear();

      (component as any).sendViewportBounds();

      expect(entities.getEntities).not.toHaveBeenCalled();
      expect(TestBed.inject(WebSocketService).sendViewportUpdate).toHaveBeenCalled();
      expect(TestBed.inject(MapViewportService).set).toHaveBeenCalled();
    });

    it('binds the clock to the bounds, range, speed, play state and seeks', () => {
      enter();
      expect(clock.startTime).toEqual({ d: at('2026-11-02T10:00:00Z') });
      expect(clock.stopTime).toEqual({ d: at('2026-11-02T11:00:00Z') });
      expect(clock.clockRange).toBe(1); // CLAMPED over a bare window
      expect(clock.currentTime).toEqual({ d: at('2026-11-02T10:00:00Z') }); // the cursor on entering

      replay.speed.set(600);
      replay.playing.set(true);
      fixture.detectChanges();
      expect(clock.multiplier).toBe(600);
      expect(clock.shouldAnimate).toBe(true);

      const range = { start: at('2026-11-02T10:10:00Z'), end: at('2026-11-02T10:20:00Z') };
      replay.range.set(range);
      replay.clockBounds.set(range);
      fixture.detectChanges();
      expect(clock.startTime).toEqual({ d: range.start });
      expect(clock.stopTime).toEqual({ d: range.end });
      expect(clock.clockRange).toBe(2); // LOOP_STOP over a range

      replay.cursor.set(at('2026-11-02T10:15:00Z'));
      fixture.detectChanges();
      expect(clock.currentTime).toEqual({ d: at('2026-11-02T10:00:00Z') }); // a cursor move alone is not a seek
      replay.seekRequested.set(1);
      fixture.detectChanges();
      expect(clock.currentTime).toEqual({ d: at('2026-11-02T10:15:00Z') });
    });

    it('leaves the clock alone in live mode', () => {
      replay.speed.set(600);
      replay.seekRequested.set(1);
      fixture.detectChanges();

      expect(clock.multiplier).toBe(1);
      expect(clock.currentTime).toBeNull();
      expect(ReplayLayer).not.toHaveBeenCalled();
    });

    it('applies the replay state once the viewer exists, for a page opened on the timeline', () => {
      (component as any).viewer = null;
      replay.mode.set('replay');
      replay.speed.set(10);
      fixture.detectChanges();
      expect(ReplayLayer).not.toHaveBeenCalled();

      (component as any).viewer = viewer;
      (component as any).bindReplayClock();

      expect(clock.onTick.addEventListener).toHaveBeenCalledTimes(1);
      expect(ReplayLayer).toHaveBeenCalledTimes(1);
      expect(clock.multiplier).toBe(10);
      expect(sources().every((s) => s.show === false)).toBe(true);
    });

    it('reports clock ticks to the service while playing, at most every 100 ms', () => {
      (component as any).bindReplayClock();
      const onTick = clock.onTick.addEventListener.mock.calls[0][0];
      const now = jest.spyOn(Date, 'now');

      clock.shouldAnimate = true;
      now.mockReturnValue(1_000);
      onTick(clock);
      expect(replay.tick).not.toHaveBeenCalled(); // live mode

      enter();
      clock.shouldAnimate = true;
      clock.currentTime = { d: at('2026-11-02T10:05:00Z') }; // where the clock has run to
      onTick(clock);
      now.mockReturnValue(1_050);
      onTick(clock);
      now.mockReturnValue(1_150);
      onTick(clock);
      expect(replay.tick).toHaveBeenCalledTimes(2);
      expect(replay.tick).toHaveBeenCalledWith(at('2026-11-02T10:05:00Z'));

      clock.shouldAnimate = false;
      now.mockReturnValue(5_000);
      onTick(clock);
      expect(replay.tick).toHaveBeenCalledTimes(2);
      now.mockRestore();
    });

    it('reports where the clock stopped when playback pauses, past the throttle', () => {
      (component as any).bindReplayClock();
      const onTick = clock.onTick.addEventListener.mock.calls[0][0];
      const now = jest.spyOn(Date, 'now');
      enter();
      replay.playing.set(true);
      fixture.detectChanges();
      clock.currentTime = { d: at('2026-11-02T10:05:00Z') };
      now.mockReturnValue(1_000);
      onTick(clock);
      clock.currentTime = { d: at('2026-11-02T10:06:00Z') };
      now.mockReturnValue(1_050);
      onTick(clock); // held back by the throttle
      expect(replay.tick).toHaveBeenCalledTimes(1);

      replay.playing.set(false);
      fixture.detectChanges();

      expect(clock.shouldAnimate).toBe(false);
      expect(replay.tick).toHaveBeenCalledTimes(2);
      expect(replay.tick).toHaveBeenLastCalledWith(at('2026-11-02T10:06:00Z'));
      now.mockRestore();
    });

    it('adds no live entity mid-replay when an emplacement disappears', () => {
      const site = baseSignalEntity({
        id: 'site-1', entityType: EntityType.SENSOR, identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }],
        position: { latitude: 10, longitude: 20 },
      } as Partial<Entity>);
      enter();
      (component as any).loadedEmplacementIds = new Set(['emp-1']);
      (component as any).emplacementEntities.set('site-1', site);

      (component as any).syncLoadedEmplacements(); // the 30 s refresh no longer lists emp-1

      expect((component as any).entityMap.size).toBe(0);
    });

    it('selects the replayed track under a click, placed at the cursor', () => {
      const actions = new Map<unknown, (e: any) => void>();
      (fakeCesium as any).ScreenSpaceEventHandler = function () {
        return { setInputAction: (fn: (e: any) => void, type: unknown) => actions.set(type, fn) };
      };
      (fakeCesium as any).ScreenSpaceEventType = { LEFT_CLICK: 'L', LEFT_DOUBLE_CLICK: 'D', MOUSE_MOVE: 'M', LEFT_DOWN: 'DN', LEFT_UP: 'UP' };
      (fakeCesium as any).defined = (x: unknown) => x != null;
      viewer.scene.canvas = document.createElement('canvas');
      viewer.scene.pick = jest.fn(() => ({ id: { id: 'replay:ac-1' } }));
      enter();
      const placed = baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT });
      layer().entityAt.mockReturnValue(placed);
      replay.cursor.set(at('2026-11-02T10:30:00Z'));

      (component as any).setupEventHandlers();
      actions.get('L')!({ position: { x: 1, y: 2 } });

      expect(layer().entityAt).toHaveBeenCalledWith('replay:ac-1', at('2026-11-02T10:30:00Z'));
      expect(component.selectedEntity()).toBe(placed);
    });

    it('restores live mode on leaving: clears the replay, resets the clock and refetches once', () => {
      enter();
      replay.playing.set(true);
      fixture.detectChanges();
      const replayLayer = layer();
      const replayed = baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT });
      (component as any).replaySelection = replayed;
      component.selectedEntity.set(replayed);
      const entities = TestBed.inject(EntityService);
      (entities.getEntities as jest.Mock).mockClear();

      replay.mode.set('live');
      replay.playing.set(false);
      fixture.detectChanges();

      expect(replayLayer.clear).toHaveBeenCalled();
      expect((component as any).replayLayer).toBeNull();
      expect(clock.shouldAnimate).toBe(false);
      expect(clock.currentTime).toEqual({ d: 'now' });
      expect(clock.clockRange).toBe(0); // UNBOUNDED
      expect(clock.multiplier).toBe(1);
      expect(sources().every((s) => s.show === true)).toBe(true);
      expect((component as any).densitySource.show).toBe(true);
      expect(entities.getEntities).toHaveBeenCalledTimes(1);
      expect(component.selectedEntity()).toBeNull(); // a replayed track is gone with the replay
      expect((component as any).inReplay()).toBe(false); // live events flow again
    });

    it('draws no drop line, flight path or bearing lines while replaying', () => {
      const bearingLayer = { sync: jest.fn(), destroy: jest.fn() };
      (component as any).bearingLayer = bearingLayer;
      const fields = {
        bearings: JSON.stringify([{ trackId: 't1', azimuthDeg: 90, elevationDeg: 0, lengthM: 1000, reason: 'no_ground_intersection', observedAt: new Date().toISOString() }]),
        bearingTtlSec: '60',
      };
      (component as any).emplacementEntities.set(
        'site-1',
        baseSignalEntity({ id: 'site-1', entityType: EntityType.SENSOR, position: { latitude: 10, longitude: 20 }, platformData: { generic: { fields } } } as Partial<Entity>),
      );
      enter();
      expect(bearingLayer.sync).toHaveBeenLastCalledWith([], expect.anything(), expect.any(Number));

      component.selectedEntity.set(baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT, position: { latitude: 50, longitude: -1, altitude: 9000 } }));
      (TestBed.inject(FlightPathService).state as any).set({
        status: 'ready',
        entityId: 'ac-1',
        path: { hex: 'abc', days: 1, points: [{ lat: 1, lon: 1, height: 1, ts: 1 }, { lat: 2, lon: 2, height: 2, ts: 2 }] },
      });
      fixture.detectChanges();

      expect((component as any).dropLine.forId).toBeNull();
      expect((component as any).flightPathLines).toHaveLength(0);
    });

    it('keeps a live selection when leaving', () => {
      const live = baseSignalEntity({ id: 'sig-1' });
      component.selectedEntity.set(live);
      enter();

      replay.mode.set('live');
      fixture.detectChanges();

      expect(component.selectedEntity()).toBe(live);
    });
  });
});
