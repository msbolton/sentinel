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
import { LayerVisibilityService } from '../../core/services/layer-visibility.service';
import { OrbitOverlayService } from '../../core/services/orbit-overlay.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { GeofenceService } from '../../core/services/geofence.service';
import { FlightPathService } from '../../core/services/flight-path.service';
import { EmplacementService } from '../../core/services/emplacement.service';
import { FeedService } from '../../core/services/feed.service';
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
    HeightReference: { NONE: 0, CLAMP_TO_GROUND: 1 },
    VerticalOrigin: { CENTER: 0, BOTTOM: 1 },
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

  beforeEach(() => {
    feedsSignal = signal<Feed[]>([]);
    fakeCesium = makeFakeCesium();
    addedEntities = [];

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
          },
        },
        { provide: ThemeService, useValue: { activeTheme$: of(ThemePreset.NORMAL) } },
        { provide: LocationService, useValue: { flyTo$: EMPTY } },
        { provide: BuildingsService, useValue: { init: jest.fn(), enabled: signal(false) } },
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
        { provide: MapViewportService, useValue: { updateBounds: jest.fn() } },
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
    viewer.scene.canvas = {};
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

  it('clears any previous selection when the clicked site is not on the stream', () => {
    const actions = new Map<unknown, (e: any) => void>();
    (fakeCesium as any).ScreenSpaceEventHandler = function () {
      return { setInputAction: (fn: (e: any) => void, type: unknown) => actions.set(type, fn) };
    };
    (fakeCesium as any).ScreenSpaceEventType = { LEFT_CLICK: 'L', LEFT_DOUBLE_CLICK: 'D', MOUSE_MOVE: 'M', LEFT_DOWN: 'DN', LEFT_UP: 'UP' };
    (fakeCesium as any).defined = (x: unknown) => x != null;
    const viewer = (component as any).viewer;
    viewer.scene.canvas = {};
    viewer.scene.pick = jest.fn(() => ({ id: { _sentinelEmplacement: 'emp-2' } }));
    component.selectedEntity.set(baseSignalEntity({ id: 'ac-1', entityType: EntityType.AIRCRAFT }));

    (component as any).setupEventHandlers();
    actions.get('L')!({ position: { x: 1, y: 2 } });

    expect(TestBed.inject(EmplacementService).select).toHaveBeenCalledWith('emp-2');
    expect(component.selectedEntity()).toBeNull();
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
    expect(TestBed.inject(Router).navigateByUrl).not.toHaveBeenCalled();
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
});
