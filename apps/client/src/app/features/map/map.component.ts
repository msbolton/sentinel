import {
  Component,
  AfterViewInit,
  OnDestroy,
  ViewChild,
  ElementRef,
  signal,
  effect,
  NgZone,
  ChangeDetectionStrategy,
  untracked,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { HttpClient } from '@angular/common/http';
import { FormsModule } from '@angular/forms';
import { Router, NavigationEnd } from '@angular/router';
import { EntityDetailPanelComponent } from '../../shared/components/entity-detail-panel.component';
import { IconComponent } from '../../shared/components/icon.component';
import { Subscription, debounceTime, Subject, throttleTime, bufferTime, filter, timer, switchMap, catchError, EMPTY } from 'rxjs';
import {
  Entity,
  EntityType,
  EntitySource,
  EntityEvent,
} from '../../shared/models/entity.model';
import { EntityService } from '../../core/services/entity.service';
import { WebSocketService } from '../../core/services/websocket.service';
import { ThemeService, ThemePreset } from '../../core/services/theme.service';
import { LocationService } from '../../core/services/location.service';
import { BuildingsService } from '../../core/services/buildings.service';
import { LayerVisibilityService } from '../../core/services/layer-visibility.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { Location } from '../../shared/models/location.model';
import {
  configureCesium,
  CESIUM_VIEWER_OPTIONS,
  ENTITY_TYPE_COLORS,
  ENTITY_TYPE_PIN_COLORS,
  ENTITY_TYPE_BILLBOARD_SVGS,
  MILITARY_AIRCRAFT_BILLBOARD_SVG,
  HEADING_ROTATED_TYPES,
  svgToDataUrl,
  DEFAULT_CAMERA_POSITION,
  TRACK_TRAIL_CONFIG,
  ORBIT_OVERLAY_CONFIG,
  DENSITY_OVERLAY_CONFIG,
  FLIGHT_PATH_CONFIG,
} from './cesium-config';
import { CircularBuffer, decimateTrail, TrailPoint } from './trail-utils';
import { isGlobalBounds, rectangleToBounds } from './viewport-bounds';
import { vesselColor, vesselHeading } from './vessel-style';
import { bucketCells, bucketDegrees, densityApplies, densityDiscSize, densityLabel } from './density-overlay';
import { DensityGrid } from '../../shared/models/density.model';
import { DeadReckoningTracker } from './dead-reckoning';
import { SatelliteTracker, periodMinutes, type SatPosition } from './satellite-tracker';
import { footprintRadiusM, sampleOrbit, splitAtAntimeridian } from './orbit-geometry';
import { OrbitOverlayService } from '../../core/services/orbit-overlay.service';
import { FlightPathService, FlightPathState } from '../../core/services/flight-path.service';
import { pathRuns } from './flight-path';
import { CLUSTER_CONFIG, CLUSTERED_TYPES, clusterDiscSpec, clusterGroupFor, drawClusterDisc, syncLabelGlyphClusterShow } from './entity-clusters';
import { DROP_LINE_CONFIG, dropLinePoints, showsDropLine, type GeoPoint } from './drop-line';
import { formatAltitude } from '../../shared/geo-format';
import { GeofenceService } from '../../core/services/geofence.service';
import { DRAFT_ID, GeofenceLayer } from './geofence-layer';
import { EMPLACEMENTS_LAYER, EMPLACEMENT_DRAFT_ID, EmplacementLayer, emplacementDrawingOf } from './emplacement-layer';
import { EmplacementService } from '../../core/services/emplacement.service';
import { emplacementIdOf } from '../../shared/models/emplacement.model';
import { GeofenceDrawTool } from './geofence-draw';
import { GeofenceToolbarComponent } from './geofence-toolbar.component';
import { DrawSession, addPoint, canFinish, draftFromSession, finishShape, newSession, setAnchor, setCursor, setRadius } from './geofence-draw-state';
import { GeofenceConfig, GeofenceShape, LngLat } from '../../shared/models/geofence.model';

/** Dead-reckoning frame cap: ~30 fps is plenty for gliding markers. */
const DR_FRAME_INTERVAL_MS = 33;
/** Pick window, in pixels, used to find the track under a ring's anchor click. */
const ANCHOR_PICK_PX = 14;

/** Two positions a double click produced from the same spot. */
function samePoint(a: LngLat, b: LngLat): boolean {
  return Math.abs(a[0] - b[0]) < 1e-6 && Math.abs(a[1] - b[1]) < 1e-6;
}

/** Whether a #rrggbb colour is light enough to need dark text on it. */
function isLightColor(hex: string): boolean {
  const h = hex.replace('#', '');
  if (h.length !== 6) return false;
  const r = parseInt(h.slice(0, 2), 16), g = parseInt(h.slice(2, 4), 16), b = parseInt(h.slice(4, 6), 16);
  return (0.299 * r + 0.587 * g + 0.114 * b) / 255 > 0.6;
}

/** All satellites are re-propagated this often; the selected one every frame. */
const SAT_STEP_INTERVAL_MS = 250;
import { environment } from '../../../environments/environment';

// Configure Cesium before imports
configureCesium();

const CRT_AMBER_SHADER = `
  uniform sampler2D colorTexture;
  in vec2 v_textureCoordinates;
  void main() {
    vec4 color = texture(colorTexture, v_textureCoordinates);
    float gray = dot(color.rgb, vec3(0.299, 0.587, 0.114));
    vec3 amber = vec3(gray * 1.0, gray * 0.7, gray * 0.2);
    out_FragColor = vec4(amber * 0.7, color.a);
  }
`;

const NVG_GREEN_SHADER = `
  uniform sampler2D colorTexture;
  in vec2 v_textureCoordinates;
  void main() {
    vec4 color = texture(colorTexture, v_textureCoordinates);
    float gray = dot(color.rgb, vec3(0.299, 0.587, 0.114));
    vec3 green = vec3(gray * 0.05, gray * 0.6, gray * 0.05);
    out_FragColor = vec4(green, color.a);
  }
`;

const FLIR_WHITE_HOT_SHADER = `
  uniform sampler2D colorTexture;
  in vec2 v_textureCoordinates;
  void main() {
    vec4 color = texture(colorTexture, v_textureCoordinates);
    float gray = dot(color.rgb, vec3(0.299, 0.587, 0.114));
    float thermal = gray * 0.9;
    vec3 whiteHot = vec3(thermal + 0.02, thermal + 0.02, thermal * 0.95 + 0.04);
    out_FragColor = vec4(whiteHot, color.a);
  }
`;

const FLIR_IRON_BOW_SHADER = `
  uniform sampler2D colorTexture;
  in vec2 v_textureCoordinates;
  void main() {
    vec4 color = texture(colorTexture, v_textureCoordinates);
    float gray = dot(color.rgb, vec3(0.299, 0.587, 0.114));
    vec3 cold = vec3(0.15, 0.05, 0.30);
    vec3 mid  = vec3(0.85, 0.25, 0.05);
    vec3 hot  = vec3(1.00, 0.85, 0.30);
    vec3 ironBow = gray < 0.5
      ? mix(cold, mid, gray * 2.0)
      : mix(mid, hot, (gray - 0.5) * 2.0);
    out_FragColor = vec4(ironBow * 0.85, color.a);
  }
`;

interface LayerConfig {
  name: string;
  entityType: EntityType;
  visible: boolean;
  color: string;
}


@Component({
  selector: 'app-map',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule, EntityDetailPanelComponent, IconComponent, GeofenceToolbarComponent],
  templateUrl: './map.component.html',
  styleUrls: ['./map.component.scss'],
})
export class MapComponent implements AfterViewInit, OnDestroy {
  @ViewChild('cesiumContainer', { static: true })
  cesiumContainer!: ElementRef<HTMLDivElement>;

  viewer: any = null;
  selectedEntity = signal<Entity | null>(null);
  showLayerPanel = signal<boolean>(false);
  flyingTo = signal<string | null>(null);
  panelRouteActive = signal<boolean>(false);

  layers: LayerConfig[] = [
    { name: 'Persons', entityType: EntityType.PERSON, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.PERSON] },
    { name: 'Vehicles', entityType: EntityType.VEHICLE, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.VEHICLE] },
    { name: 'Vessels', entityType: EntityType.VESSEL, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.VESSEL] },
    { name: 'Aircraft', entityType: EntityType.AIRCRAFT, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.AIRCRAFT] },
    { name: 'Drones', entityType: EntityType.DRONE, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.DRONE] },
    { name: 'Facilities', entityType: EntityType.FACILITY, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.FACILITY] },
    { name: 'Equipment', entityType: EntityType.EQUIPMENT, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.EQUIPMENT] },
    { name: 'Units', entityType: EntityType.UNIT, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.UNIT] },
    { name: 'Signals', entityType: EntityType.SIGNAL, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.SIGNAL] },
    { name: 'Cyber', entityType: EntityType.CYBER, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.CYBER] },
    { name: 'Satellites', entityType: EntityType.SATELLITE, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.SATELLITE] },
    { name: 'Unknown', entityType: EntityType.UNKNOWN, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.UNKNOWN] },
  ];

  private Cesium: any;
  private userLocationEntity: any = null;
  private entityMap = new Map<string, any>();
  private trackTrails = new Map<string, CircularBuffer<{ lat: number; lon: number; alt: number }>>();
  private subscriptions = new Subscription();
  private cameraMovedSubject = new Subject<void>();
  private cesiumColorCache = new Map<string, any>();
  private billboardImageCache = new Map<string, string>();
  private renderScheduled = false;
  private currentCameraAltitude = DEFAULT_CAMERA_POSITION.height;
  /** Moving aircraft are advanced between fixes; see dead-reckoning.ts. */
  private readonly deadReckoning = new DeadReckoningTracker();
  /** Satellites are propagated from their elements; see satellite-tracker.ts. */
  private readonly satellites = new SatelliteTracker();
  private satLastStepMs = 0;
  /** Overlays for the selected satellite; see orbit-geometry.ts. */
  private orbit: { forId: string | null; path: any | null; ground: any[]; footprint: any | null; builtAtMs: number; line1: string } =
    { forId: null, path: null, ground: [], footprint: null, builtAtMs: 0, line1: '' };
  /** One clustered data source per type in CLUSTERED_TYPES; see entity-clusters.ts. */
  private clusterSources = new Map<string, any>();
  /** Saved geofences drawn on the globe; see geofence-layer.ts. */
  private geofenceLayer: GeofenceLayer | null = null;
  private emplacementLayer: EmplacementLayer | null = null;
  /** The SENSOR entities that stand for loaded emplacements, by entity id; the emplacement layer draws them. */
  private readonly emplacementEntities = new Map<string, Entity>();
  /** The ids of the emplacements loaded now; only their entities are left to the emplacement layer. */
  private loadedEmplacementIds: ReadonlySet<string> = new Set();
  /** The geofence being drawn, or null; see geofence-draw-state.ts and geofence-draw.ts. */
  readonly drawSession = signal<DrawSession | null>(null);
  private drawTool: GeofenceDrawTool | null = null;
  /** A geofence handle being dragged, with the config it has reached. */
  private handleDrag: { zoneId: string; index: number; config: GeofenceConfig | null } | null = null;
  private clusterImageCache = new Map<string, string>();
  private clusterCanvas: HTMLCanvasElement | null = null;
  /** The line from the selected aircraft, drone or satellite down to the ground; see drop-line.ts. */
  private dropLine: { forId: string | null; line: any; label: any; positions: any[]; labelText: string } =
    { forId: null, line: null, label: null, positions: [], labelText: '' };
  private drFrame: number | null = null;
  private drLastTickMs = 0;
  private drScratch: any = null;
  private themePostProcessStage: any = null;

  /** Cached ageout configs fetched from backend, refreshed every 60s */
  private ageoutConfigs: Array<{ sourceType: string | null; staleThresholdMs: number }> = [];
  private ageoutConfigTimer: ReturnType<typeof setInterval> | null = null;
  /** The latest vessel density grid and when it arrived (local clock). */
  private density: { grid: DensityGrid; receivedAt: number } | null = null;
  private densitySource: any = null;
  private densityShown = false;
  private densityTimer: ReturnType<typeof setInterval> | null = null;
  /** The selected aircraft's past path: polylines and what they were drawn from. */
  private flightPathLines: any[] = [];
  private flightPathKey = '';

  constructor(
    private readonly ngZone: NgZone,
    private readonly router: Router,
    private readonly entityService: EntityService,
    private readonly wsService: WebSocketService,
    private readonly themeService: ThemeService,
    private readonly locationService: LocationService,
    readonly buildingsService: BuildingsService,
    private readonly http: HttpClient,
    private readonly layerVisibility: LayerVisibilityService,
    private readonly orbitOverlay: OrbitOverlayService,
    private readonly mapViewport: MapViewportService,
    private readonly geofences: GeofenceService,
    private readonly flightPath: FlightPathService,
    private readonly emplacements: EmplacementService,
  ) {
    // Entities stay in the viewer when their layer is hidden (show=false) so
    // dead reckoning keeps advancing them; this flips `show` when the user's
    // hidden set changes.
    effect(() => {
      this.layerVisibility.hidden();
      this.ngZone.runOutsideAngular(() => this.applyLayerVisibility());
    });

    // The selected satellite gets its orbit path, ground track and
    // footprint; the detail panel's toggles decide which are shown.
    effect(() => {
      const selected = this.selectedEntity();
      const flags = { path: this.orbitOverlay.showPath(), ground: this.orbitOverlay.showGroundTrack(), footprint: this.orbitOverlay.showFootprint() };
      this.ngZone.runOutsideAngular(() => untracked(() => this.syncOrbitOverlay(selected, flags)));
    });

    // The selected aircraft's past path from IntelSky: the service follows
    // the selection and the panel's range; the map draws what it loaded.
    effect(() => {
      const selected = this.selectedEntity();
      untracked(() => this.flightPath.select(selected));
    });
    effect(() => {
      const state = this.flightPath.state();
      this.ngZone.runOutsideAngular(() => untracked(() => this.syncFlightPath(state)));
    });

    // The panel's New button and the map control ask for a new zone here.
    this.subscriptions.add(this.geofences.startDrawingRequested.subscribe(() => { if (this.geofences.canWrite()) this.startDrawing(); }));
    effect(() => {
      const session = this.drawSession();
      this.ngZone.runOutsideAngular(() => {
        if (session) this.drawTool?.render(session);
        else this.drawTool?.clear();
      });
    });

    // Saved geofences are drawn on the globe and follow the service's list and selection.
    effect(() => {
      const zones = this.geofences.zones();
      const selectedId = this.geofences.selectedId();
      const draft = this.geofences.draft();
      this.geofences.anchors();
      this.ngZone.runOutsideAngular(() => untracked(() => this.geofenceLayer?.sync(zones, selectedId, (id) => this.geofenceAnchorPosition(id), draft)));
    });

    // Emplacements, their coverage and the form's unsaved preview, on the
    // built-in emplacements feed's layer.
    effect(() => {
      // Read the signals here so the effect re-runs on each; the method re-reads them untracked.
      this.emplacements.emplacements();
      this.emplacements.selectedId();
      this.emplacements.draft();
      this.emplacements.showCoverage();
      this.layerVisibility.isVisible(EMPLACEMENTS_LAYER);
      untracked(() => {
        this.syncLoadedEmplacements();
        this.resyncEmplacements();
      });
    });
    // Emplacements change rarely; refresh them every 30 s while their layer is shown.
    this.subscriptions.add(
      timer(0, 30_000)
        .pipe(
          filter(() => this.layerVisibility.isVisible(EMPLACEMENTS_LAYER)),
          switchMap(() => this.emplacements.load().pipe(catchError(() => EMPTY))),
        )
        .subscribe(),
    );

    // A selected aircraft, drone or satellite shows how high it flies: a line to the ground.
    effect(() => {
      const selected = this.selectedEntity();
      this.ngZone.runOutsideAngular(() => untracked(() => this.syncDropLine(selected)));
    });

    // Track whether a panel route is active (anything other than /map or /)
    const routerSub = this.router.events.pipe(
      filter((e): e is NavigationEnd => e instanceof NavigationEnd),
    ).subscribe((e) => {
      const url = e.urlAfterRedirects ?? e.url;
      const isPanelRoute = url !== '/map' && url !== '/' && url !== '';
      this.panelRouteActive.set(isPanelRoute);
    });
    this.subscriptions.add(routerSub);

    // Check initial route
    const currentUrl = this.router.url;
    this.panelRouteActive.set(currentUrl !== '/map' && currentUrl !== '/' && currentUrl !== '');
  }

  async ngAfterViewInit(): Promise<void> {
    await this.initCesium();
    this.geofences.load().subscribe({ error: () => undefined });
    this.setupEventHandlers();
    this.subscribeToEntities();
    this.setupCameraMoveHandler();

    // Initial entity fetch — computeViewRectangle() returns undefined at high
    // altitude (view covers more than a hemisphere), so the camera moveEnd
    // handler won't trigger a fetch.  Load all entities eagerly.
    // Render directly from the response to bypass the throttled
    // currentEntities$ subscription which drops the first emission.
    this.entityService.getEntities({ pageSize: 500 }).subscribe((response) => {
      this.ngZone.runOutsideAngular(() => {
        for (const entity of response.data) {
          this.addOrUpdateCesiumEntity(entity);
        }
        this.scheduleRender();
      });
    });

    this.fetchAgeoutConfigs();
    this.ageoutConfigTimer = setInterval(() => this.fetchAgeoutConfigs(), 60_000);

    this.buildingsService.init(this.viewer, this.Cesium);
    this.subscribeToThemeChanges();
    this.subscribeToFlyTo();
    this.subscribeToDensity();
    this.initUserLocation();

    // Report the initial viewport: a saved camera position is restored with
    // setView, which does not fire moveEnd. A later flyTo (geolocation)
    // fires moveEnd and refreshes it.
    this.sendViewportBounds();
  }

  ngOnDestroy(): void {
    this.geofenceLayer?.destroy();
    this.geofenceLayer = null;
    this.emplacementLayer?.destroy();
    this.emplacementLayer = null;
    this.clearOrbitOverlay();
    this.clearFlightPath();
    if (this.ageoutConfigTimer) clearInterval(this.ageoutConfigTimer);
    if (this.densityTimer) clearInterval(this.densityTimer);
    if (this.drFrame !== null) {
      cancelAnimationFrame(this.drFrame);
      this.drFrame = null;
    }
    this.deadReckoning.clear();
        this.satellites.clear();
    this.subscriptions.unsubscribe();
    if (this.viewer && !this.viewer.isDestroyed()) {
      this.viewer.destroy();
    }
  }

  private async initCesium(): Promise<void> {
    const Cesium = await import('cesium');
    this.Cesium = Cesium;

    // Set Ion access token before creating the viewer so the default
    // imagery provider (Bing Maps via Ion) can authenticate.
    if (environment.cesiumIonAccessToken) {
      Cesium.Ion.defaultAccessToken = environment.cesiumIonAccessToken;
    }

    // Wait until the container has non-zero dimensions.  Angular may not have
    // completed style application / layout by ngAfterViewInit, which causes
    // Cesium's "Expected width to be greater than 0" DeveloperError.
    const container = this.cesiumContainer.nativeElement;
    await this.waitForLayout(container);

    // Create a hidden credit container
    const creditContainer = document.createElement('div');
    creditContainer.style.display = 'none';
    container.appendChild(creditContainer);

    const viewerOptions = {
      ...CESIUM_VIEWER_OPTIONS,
      creditContainer,
    };

    this.ngZone.runOutsideAngular(() => {
      this.viewer = new Cesium.Viewer(container, viewerOptions);
      this.setupClusterSources();
      this.geofenceLayer = new GeofenceLayer(this.viewer, Cesium);
      this.emplacementLayer = new EmplacementLayer(this.viewer, Cesium);
      this.drawTool = new GeofenceDrawTool(this.viewer, Cesium);
      this.geofenceLayer.sync(this.geofences.zones(), this.geofences.selectedId(), (id) => this.geofenceAnchorPosition(id));
      this.resyncEmplacements();

      // Set dark atmosphere
      this.viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#0a0e17');
      this.viewer.scene.globe.baseColor = Cesium.Color.fromCssColorString('#0a0e17');

      // Disable expensive visual features
      if (this.viewer.scene.sun) this.viewer.scene.sun.show = false;
      if (this.viewer.scene.moon) this.viewer.scene.moon.show = false;
      this.viewer.scene.fog.enabled = false;
      this.viewer.scene.globe.showGroundAtmosphere = false;
      this.viewer.scene.globe.enableLighting = false;
      this.viewer.scene.postProcessStages.fxaa.enabled = false;

      // Increase screen space error tolerance for fewer terrain tiles
      this.viewer.scene.globe.maximumScreenSpaceError = 4;

      // Larger tile cache to reduce network requests
      this.viewer.scene.globe.tileCacheSize = 1000;

      // Enable depth testing for entities behind the globe
      this.viewer.scene.globe.depthTestAgainstTerrain = false;

      // Set initial camera position
      this.viewer.camera.setView({
        destination: Cesium.Cartesian3.fromDegrees(
          DEFAULT_CAMERA_POSITION.longitude,
          DEFAULT_CAMERA_POSITION.latitude,
          DEFAULT_CAMERA_POSITION.height,
        ),
      });

      // Force render
      this.viewer.scene.requestRender();
    });
  }

  private setupEventHandlers(): void {
    if (!this.viewer || !this.Cesium) return;

    const Cesium = this.Cesium;
    const handler = new Cesium.ScreenSpaceEventHandler(this.viewer.scene.canvas);

    // Left click - select entity
    handler.setInputAction((click: any) => {
      const pickedObject = this.viewer.scene.pick(click.position);

      if (this.emplacements.picking()) {
        const p = this.pickLngLat(click.position);
        if (p) this.ngZone.run(() => this.emplacements.pickAt(p[1], p[0]));
        return;
      }

      if (this.drawSession()) {
        this.drawClick(click.position, pickedObject);
        this.viewer.selectedEntity = undefined; // the viewer's own bracket means nothing while drawing
        return;
      }

      if (Cesium.defined(pickedObject) && (pickedObject.id?._sentinelOrbit || pickedObject.id?._sentinelFlightPath)) {
        return; // a click on the selection's own overlay keeps the selection
      }
      if (Cesium.defined(pickedObject) && pickedObject.id?._sentinelGeofence) {
        const zoneId = pickedObject.id._sentinelGeofence as string;
        this.viewer.selectedEntity = undefined; // the zone's handles mark the selection, not the viewer's bracket
        if (zoneId !== DRAFT_ID) this.ngZone.run(() => this.geofences.select(zoneId));
        return;
      }
      if (Cesium.defined(pickedObject) && pickedObject.id?._sentinelEmplacement) {
        const id = pickedObject.id._sentinelEmplacement as string;
        this.viewer.selectedEntity = undefined;
        if (id !== EMPLACEMENT_DRAFT_ID) {
          this.ngZone.run(() => {
            this.emplacements.select(id);
            this.router.navigateByUrl('/emplacements');
          });
        }
        return;
      }
      if (Cesium.defined(pickedObject) && pickedObject.id?._sentinelDensity) {
        const { lat, lon } = pickedObject.id._sentinelDensity as { lat: number; lon: number };
        this.viewer.camera.flyTo({ destination: Cesium.Cartesian3.fromDegrees(lon, lat, DENSITY_OVERLAY_CONFIG.zoomHeightM) });
        return;
      }
      if (Cesium.defined(pickedObject) && pickedObject.id?._sentinelCluster) {
        this.flyToCluster(pickedObject.id._sentinelCluster);
        return;
      }
      if (Cesium.defined(pickedObject) && (pickedObject.id?._sentinelDropLine || pickedObject.id?._sentinelGeofenceHandle)) {
        return; // a click on a handle is the start of a drag, not a deselection
      }
      if (Cesium.defined(pickedObject) && pickedObject.id?._sentinelEntity) {
        this.ngZone.run(() => {
          this.selectedEntity.set(pickedObject.id._sentinelEntity);
        });
      } else {
        this.ngZone.run(() => {
          this.selectedEntity.set(null);
        });
      }
    }, Cesium.ScreenSpaceEventType.LEFT_CLICK);

    // Drawing: a double click closes a polygon; the mouse carries the trailing edge.
    handler.setInputAction(() => {
      const session = this.drawSession();
      if (session) this.viewer.trackedEntity = undefined; // the viewer's own double click must not start tracking a track mid-draw
      if (!session || session.shape !== 'polygon') return;
      // the two single clicks of a double click added the same point twice
      const points = session.points.length >= 2 && samePoint(session.points[session.points.length - 1], session.points[session.points.length - 2])
        ? session.points.slice(0, -1)
        : session.points;
      const trimmed = { ...session, points };
      if (canFinish(trimmed).ok) this.ngZone.run(() => { this.drawSession.set(trimmed); this.finishDrawing(); });
    }, Cesium.ScreenSpaceEventType.LEFT_DOUBLE_CLICK);
    handler.setInputAction((move: any) => {
      if (this.handleDrag) {
        const p = this.pickLngLat(move.endPosition);
        if (p) this.handleDrag.config = this.geofenceLayer?.moveHandle(this.handleDrag.zoneId, this.handleDrag.index, p) ?? null;
        return;
      }
      const session = this.drawSession();
      if (!session) return;
      const p = this.pickLngLat(move.endPosition);
      this.drawSession.set(setCursor(session, p));
    }, Cesium.ScreenSpaceEventType.MOUSE_MOVE);

    // Reshaping a selected geofence: a drag that starts on one of its handles
    // moves the handle and pauses camera rotation for exactly that drag.
    handler.setInputAction((down: any) => {
      if (this.drawSession()) return;
      const handle = this.geofenceLayer?.handleAt(this.viewer.scene.pick(down.position));
      if (!handle) return;
      this.handleDrag = { ...handle, config: null };
      const controller = this.viewer.scene.screenSpaceCameraController;
      controller.enableRotate = false;
      controller.enableTranslate = false;
    }, Cesium.ScreenSpaceEventType.LEFT_DOWN);
    handler.setInputAction(() => {
      const drag = this.handleDrag;
      if (!drag) return;
      this.handleDrag = null;
      const controller = this.viewer.scene.screenSpaceCameraController;
      controller.enableRotate = true;
      controller.enableTranslate = true;
      if (drag.config) {
        const config = drag.config;
        // a refused drag snaps the shape back to what the server holds
        this.ngZone.run(() => this.geofences.update(drag.zoneId, { config }).subscribe({ error: () => this.resyncGeofences() }));
      }
    }, Cesium.ScreenSpaceEventType.LEFT_UP);
  }

  private setupCameraMoveHandler(): void {
    if (!this.viewer) return;

    // Debounce camera movements to avoid spamming the WebSocket
    const sub = this.cameraMovedSubject
      .pipe(debounceTime(500))
      .subscribe(() => {
        this.sendViewportBounds();
        this.refreshTrailDecimation();
      });
    this.subscriptions.add(sub);

    this.viewer.camera.moveEnd.addEventListener(() => {
      this.cameraMovedSubject.next();
      this.saveCameraPosition();
    });
  }

  private sendViewportBounds(): void {
    if (!this.viewer || !this.Cesium) return;

    const Cesium = this.Cesium;
    const camera = this.viewer.camera;

    let rect: { north: number; south: number; east: number; west: number } | undefined;
    try {
      rect = camera.computeViewRectangle(this.viewer.scene.globe.ellipsoid, new Cesium.Rectangle());
    } catch {
      // Camera may not have a valid view rectangle (e.g., looking at sky);
      // treat it as the whole globe so the server still knows our state.
      rect = undefined;
    }

    // Always tell the gateway what we see, including the whole globe:
    // the server keeps the previous bounds otherwise, and the ingest area
    // is derived from these bounds.
    const bounds = rectangleToBounds(rect, Cesium.Math.toDegrees);
    this.mapViewport.set(bounds);
    this.syncDensity();
    this.wsService.sendViewportUpdate(bounds);

    // Also fetch entities in this viewport (no bbox when the globe is visible).
    const query = isGlobalBounds(bounds) ? { pageSize: 500 } : { ...bounds, pageSize: 500 };
    this.entityService.getEntities(query).subscribe((response) => {
      this.ngZone.runOutsideAngular(() => {
        for (const entity of response.data) {
          this.addOrUpdateCesiumEntity(entity);
        }
        this.scheduleRender();
      });
    });
  }

  private subscribeToEntities(): void {
    // Subscribe to real-time entity updates — batch within 100ms windows
    const sub = this.entityService.entityUpdates$.pipe(
      bufferTime(100),
      filter((batch) => batch.length > 0),
    ).subscribe((batch) => {
      this.ngZone.runOutsideAngular(() => {
        this.processCesiumBatch(batch);
      });
    });
    this.subscriptions.add(sub);

    // Subscribe to the full entity state — throttle to at most once/sec
    const entityStateSub = this.entityService.currentEntities$.pipe(
      throttleTime(1000),
    ).subscribe((entities) => {
      this.ngZone.runOutsideAngular(() => {
        if (!this.viewer) return;
        this.suspendCollectionEvents();
        try {
          entities.forEach((entity, id) => {
            if (!this.entityMap.has(id)) {
              this.addOrUpdateCesiumEntity(entity);
            }
          });
        } finally {
          this.resumeCollectionEvents();
        }
        this.scheduleRender();
      });
    });
    this.subscriptions.add(entityStateSub);

    // Subscribe to entity evictions — remove stale entities from the globe
    const evictionSub = this.entityService.entityEvictions$.subscribe((ids) => {
      this.ngZone.runOutsideAngular(() => {
        if (!this.viewer) return;
        this.suspendCollectionEvents();
        try {
          for (const id of ids) {
            this.removeCesiumEntity(id);
          }
        } finally {
          this.resumeCollectionEvents();
        }
        this.scheduleRender();
      });
    });
    this.subscriptions.add(evictionSub);
  }

  private processCesiumBatch(events: EntityEvent[]): void {
    if (!this.viewer) return;

    console.debug(`[Map] Processing batch of ${events.length} entity events`);

    this.suspendCollectionEvents();
    try {
      for (const event of events) {
        this.handleEntityEvent(event);
      }
    } catch (err) {
      console.error('[Map] Error processing entity batch:', err);
    } finally {
      this.resumeCollectionEvents();
    }

    console.debug(`[Map] Entity map size: ${this.entityMap.size}, viewer entities: ${this.viewer.entities.values.length}`);
    this.scheduleRender();
  }

  private subscribeToThemeChanges(): void {
    const sub = this.themeService.activeTheme$.subscribe((theme) => {
      this.ngZone.runOutsideAngular(() => {
        this.applyThemeToGlobe(theme);
      });
    });
    this.subscriptions.add(sub);
  }

  private applyThemeToGlobe(theme: ThemePreset): void {
    if (!this.viewer || !this.Cesium) return;

    const Cesium = this.Cesium;

    // Remove any existing theme post-process stage
    if (this.themePostProcessStage) {
      this.viewer.scene.postProcessStages.remove(this.themePostProcessStage);
      this.themePostProcessStage = null;
    }

    if (theme === ThemePreset.CRT) {
      this.viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#0d0a00');
      this.viewer.scene.globe.baseColor = Cesium.Color.fromCssColorString('#0d0a00');
      this.themePostProcessStage = new Cesium.PostProcessStage({
        fragmentShader: CRT_AMBER_SHADER,
      });
      this.viewer.scene.postProcessStages.add(this.themePostProcessStage);
    } else if (theme === ThemePreset.NIGHT_VISION) {
      this.viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#010a01');
      this.viewer.scene.globe.baseColor = Cesium.Color.fromCssColorString('#010a01');
      this.themePostProcessStage = new Cesium.PostProcessStage({
        fragmentShader: NVG_GREEN_SHADER,
      });
      this.viewer.scene.postProcessStages.add(this.themePostProcessStage);
    } else if (theme === ThemePreset.FLIR) {
      this.viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#08081a');
      this.viewer.scene.globe.baseColor = Cesium.Color.fromCssColorString('#08081a');
      this.themePostProcessStage = new Cesium.PostProcessStage({
        fragmentShader: FLIR_WHITE_HOT_SHADER,
      });
      this.viewer.scene.postProcessStages.add(this.themePostProcessStage);
    } else if (theme === ThemePreset.FLIR_IRON_BOW) {
      this.viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#08081a');
      this.viewer.scene.globe.baseColor = Cesium.Color.fromCssColorString('#08081a');
      this.themePostProcessStage = new Cesium.PostProcessStage({
        fragmentShader: FLIR_IRON_BOW_SHADER,
      });
      this.viewer.scene.postProcessStages.add(this.themePostProcessStage);
    } else {
      this.viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#0a0e17');
      this.viewer.scene.globe.baseColor = Cesium.Color.fromCssColorString('#0a0e17');
    }

    this.viewer.scene.requestRender();
  }

  private handleEntityEvent(event: EntityEvent): void {
    switch (event.type) {
      case 'created':
      case 'updated':
        this.addOrUpdateCesiumEntity(event.entity);
        break;
      case 'deleted':
        this.removeCesiumEntity(event.entity.id);
        break;
    }
  }

  private addOrUpdateCesiumEntity(entity: Entity): void {
    if (!this.viewer || !this.Cesium) return;
    // A loaded emplacement's own SENSOR entity is drawn by the emplacement layer.
    if (emplacementDrawingOf(entity, this.loadedEmplacementIds)) {
      this.emplacementEntities.set(entity.id, entity);
      return;
    }
    if (!entity.position) {
      console.warn(`[Map] Entity ${entity.id} has no position, skipping`);
      return;
    }

    // Don't render aged-out entities
    if (entity.ageoutState === 'AGED_OUT') {
      this.removeCesiumEntity(entity.id);
      return;
    }

    const Cesium = this.Cesium;
    const layer = this.layers.find((l) => l.entityType === entity.entityType);
    if (layer && !layer.visible) return;

    const cesiumColor = this.entityColor(entity);
    const isStale = this.isEntityStale(entity);
    const billboardAlpha = isStale ? 0.4 : 1.0;
    const trailAlpha = isStale ? TRACK_TRAIL_CONFIG.trailOpacity * 0.4 : TRACK_TRAIL_CONFIG.trailOpacity;

    // Track trail
    this.updateTrackTrail(entity);
    const trailBuffer = this.trackTrails.get(entity.id);
    const rawTrail = trailBuffer ? trailBuffer.toArray() : [];
    const trail = decimateTrail(rawTrail, this.currentCameraAltitude, TRACK_TRAIL_CONFIG.decimation);

    const position = Cesium.Cartesian3.fromDegrees(
      entity.position.longitude,
      entity.position.latitude,
      entity.position.altitude ?? 0,
    );

    const hasAltitude = entity.position.altitude != null && entity.position.altitude > 0;
    const heightRef = hasAltitude
      ? Cesium.HeightReference.NONE
      : Cesium.HeightReference.CLAMP_TO_GROUND;

    // Vessels point along their heading only while moving or when they report a true heading.
    const headingDeg = entity.entityType === EntityType.VESSEL
      ? vesselHeading(entity)
      : HEADING_ROTATED_TYPES.has(entity.entityType) ? entity.heading ?? null : null;
    const rotation = headingDeg != null ? -Cesium.Math.toRadians(headingDeg) : 0;

    const existing = this.entityMap.get(entity.id);
    const shown = this.layerVisibility.isVisible(entity);

    // Moving aircraft are dead-reckoned: the tracker owns their displayed
    // position and blends this fix in instead of jumping to it.
    const now = this.wsService.serverNow();
    const tracked = this.deadReckoning.upsert(entity, now);
    const satTracked = this.satellites.upsert(entity);
    if (satTracked && entity.id === this.orbit.forId) {
      const line1 = (entity.platformData?.['tle'] as { line1?: string } | undefined)?.line1 ?? '';
      if (line1 !== this.orbit.line1) this.orbit.builtAtMs = 0; // elements changed: re-sample on the next frame
    }
    const shownPosition = tracked ? this.deadReckoning.positionOf(entity.id, now) : null;
    const satPosition = satTracked ? this.satellites.positionOf(entity.id, now) : null;
    const displayPosition = shownPosition
      ? Cesium.Cartesian3.fromDegrees(shownPosition.lon, shownPosition.lat, shownPosition.alt)
      : satPosition
        ? Cesium.Cartesian3.fromDegrees(satPosition.lon, satPosition.lat, satPosition.height)
        : position;

    const labelText = satPosition ? this.satelliteLabel(entity.name, satPosition.height) : entity.name;

    if (existing) {
      // Update existing entity
      existing.show = shown;
      existing._posProp.setValue(displayPosition);
      if (existing._labelText !== labelText) {
        existing._labelText = labelText;
        existing.label.text = labelText;
      }
      if (existing.billboard) {
        existing.billboard.rotation = rotation;
        existing.billboard.color = (this.isMilitaryAircraft(entity) ? this.Cesium.Color.WHITE : cesiumColor).withAlpha(billboardAlpha);
      }
      if (existing.label) {
        existing.label.fillColor = this.Cesium.Color.WHITE.withAlpha(billboardAlpha);
      }
      // An entity first seen without an altitude was clamped to ground;
      // lift it once a fix with altitude arrives (and vice versa).
      if (existing._heightRef !== heightRef) {
        existing._heightRef = heightRef;
        if (existing.billboard) existing.billboard.heightReference = heightRef;
        if (existing.label) existing.label.heightReference = heightRef;
      }

      // Update polyline trail
      if (trail.length >= 2) {
        const trailPositions = trail.map((p: TrailPoint) =>
          Cesium.Cartesian3.fromDegrees(p.lon, p.lat, p.alt),
        );
        if (existing.polyline) {
          existing.polyline.positions = trailPositions;
        } else {
          existing.polyline = new Cesium.PolylineGraphics({
            positions: trailPositions,
            width: TRACK_TRAIL_CONFIG.width,
            material: cesiumColor.withAlpha(trailAlpha),
            clampToGround: !hasAltitude,
          });
        }
      }

      // Update stored sentinel entity data
      existing._sentinelEntity = entity;
    } else {
      // Create new entity. The position is a property we keep a handle on so
      // the dead-reckoning loop can move it without touching the entity.
      const posProp = new Cesium.ConstantPositionProperty(displayPosition);
      const cesiumEntity = this.collectionFor(entity.entityType).add({
        position: posProp,
        show: shown,
        billboard: {
          image: this.getBillboardImage(entity),
          scale: 0.5,
          color: (this.isMilitaryAircraft(entity) ? Cesium.Color.WHITE : cesiumColor).withAlpha(billboardAlpha),
          heightReference: heightRef,
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
          verticalOrigin: Cesium.VerticalOrigin.CENTER,
          rotation,
          alignedAxis: Cesium.Cartesian3.UNIT_Z,
        },
        label: {
          text: labelText,
          font: '12px JetBrains Mono, monospace',
          fillColor: Cesium.Color.WHITE.withAlpha(billboardAlpha),
          outlineColor: Cesium.Color.BLACK,
          outlineWidth: 2,
          style: Cesium.LabelStyle.FILL_AND_OUTLINE,
          verticalOrigin: Cesium.VerticalOrigin.BOTTOM,
          pixelOffset: new Cesium.Cartesian2(0, -16),
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
          showBackground: true,
          backgroundColor: Cesium.Color.fromCssColorString('#0a0e17').withAlpha(0.7),
          backgroundPadding: new Cesium.Cartesian2(6, 4),
          heightReference: heightRef,
        },
        polyline:
          trail.length >= 2
            ? {
                positions: trail.map((p: TrailPoint) =>
                  Cesium.Cartesian3.fromDegrees(p.lon, p.lat, p.alt),
                ),
                width: TRACK_TRAIL_CONFIG.width,
                material: cesiumColor.withAlpha(trailAlpha),
                clampToGround: !hasAltitude,
              }
            : undefined,
      });

      // Store reference to SENTINEL entity data
      cesiumEntity._sentinelEntity = entity;
      cesiumEntity._posProp = posProp;
      cesiumEntity._heightRef = heightRef;
      cesiumEntity._labelText = labelText;
      this.entityMap.set(entity.id, cesiumEntity);
    }

    if (entity.id === this.dropLine.forId) {
      const shownGeo = this.displayedGeoPoint(entity, now);
      if (shownGeo) this.moveDropLine(shownGeo);
    }

    if (tracked || satTracked) {
      this.ensureDeadReckoningLoop();
    }
    this.scheduleRender();
  }

  /**
   * Run a requestAnimationFrame loop while any entity is dead-reckoned,
   * advancing displayed positions between fixes. The loop stops itself once
   * nothing is tracked so an idle map renders nothing.
   */
  private ensureDeadReckoningLoop(): void {
    if (this.drFrame !== null || (this.deadReckoning.size === 0 && this.satellites.size === 0)) return;
    this.ngZone.runOutsideAngular(() => {
      const tick = () => {
        this.drFrame = null;
        if (!this.viewer || this.viewer.isDestroyed() || (this.deadReckoning.size === 0 && this.satellites.size === 0)) return;
        const now = this.wsService.serverNow();
        if (now - this.drLastTickMs >= DR_FRAME_INTERVAL_MS) {
          this.drLastTickMs = now;
          this.applyDeadReckoning(now);
        }
        this.drFrame = requestAnimationFrame(tick);
      };
      this.drFrame = requestAnimationFrame(tick);
    });
  }

  private applyDeadReckoning(now: number): void {
    const Cesium = this.Cesium;
    if (!this.drScratch) this.drScratch = new Cesium.Cartesian3();
    let moved = 0;
    for (const [id, pos] of this.deadReckoning.step(now)) {
      const cesiumEntity = this.entityMap.get(id);
      if (!cesiumEntity?._posProp || cesiumEntity.show === false) {
        if (!cesiumEntity) this.deadReckoning.remove(id);
        continue;
      }
      // setValue clones, so the scratch Cartesian3 is safe to reuse.
      cesiumEntity._posProp.setValue(
        Cesium.Cartesian3.fromDegrees(pos.lon, pos.lat, pos.alt, undefined, this.drScratch),
      );
      if (id === this.dropLine.forId) this.moveDropLine({ lat: pos.lat, lon: pos.lon, height: pos.alt });
      moved++;
    }
    moved += this.applySatelliteMotion(now);
    if (moved > 0) {
      this.viewer.scene.requestRender();
    }
  }

  /**
   * Satellites are propagated every SAT_STEP_INTERVAL_MS; the selected one
   * every frame so it never visibly jumps while it is being watched.
   */
  private applySatelliteMotion(now: number): number {
    if (this.satellites.size === 0) return 0;
    const Cesium = this.Cesium;
    const selectedId = untracked(() => this.selectedEntity())?.id;
    const stepAll = now - this.satLastStepMs >= SAT_STEP_INTERVAL_MS;
    if (stepAll) this.satLastStepMs = now;
    let moved = 0;
    const place = (id: string, pos: { lat: number; lon: number; height: number }) => {
      const cesiumEntity = this.entityMap.get(id);
      if (!cesiumEntity?._posProp) {
        if (!cesiumEntity) this.satellites.remove(id);
        return;
      }
      if (cesiumEntity.show === false) return;
      cesiumEntity._posProp.setValue(Cesium.Cartesian3.fromDegrees(pos.lon, pos.lat, pos.height, undefined, this.drScratch));
      const text = this.satelliteLabel(cesiumEntity._sentinelEntity?.name ?? '', pos.height);
      if (cesiumEntity._labelText !== text && cesiumEntity.label) {
        cesiumEntity._labelText = text;
        cesiumEntity.label.text = text;
      }
      if (id === this.dropLine.forId) this.moveDropLine(pos);
      moved++;
      if (id === this.orbit.forId) this.updateOrbitOverlay(id, pos, now);
    };
    if (stepAll) {
      for (const [id, pos] of this.satellites.step(now)) place(id, pos);
    } else if (selectedId && this.satellites.has(selectedId)) {
      const pos = this.satellites.positionOf(selectedId, now);
      if (pos) place(selectedId, pos);
    }
    return moved;
  }

  /** Build, show/hide or clear the overlays to match the selection and the toggles. */
  private syncOrbitOverlay(selected: Entity | null, flags: { path: boolean; ground: boolean; footprint: boolean }): void {
    if (!this.viewer || this.viewer.isDestroyed()) return;
    if (!selected || selected.entityType !== EntityType.SATELLITE || !this.satellites.has(selected.id)) {
      this.clearOrbitOverlay();
      return;
    }
    if (this.orbit.forId !== selected.id) {
      this.clearOrbitOverlay();
      this.orbit.forId = selected.id;
      this.orbit.line1 = (selected.platformData?.['tle'] as { line1?: string } | undefined)?.line1 ?? '';
      const now = this.wsService.serverNow();
      const pos = this.satellites.positionOf(selected.id, now);
      if (pos) this.buildOrbitOverlay(selected.id, pos, now);
      this.ensureDeadReckoningLoop();
    }
    if (this.orbit.path) this.orbit.path.show = flags.path;
    for (const g of this.orbit.ground) g.show = flags.ground;
    if (this.orbit.footprint) this.orbit.footprint.show = flags.footprint;
    this.scheduleRender();
  }

  private buildOrbitOverlay(id: string, pos: SatPosition, now: number): void {
    const Cesium = this.Cesium;
    const satrec = this.satellites.satrecOf(id);
    if (!satrec) return;
    const cfg = ORBIT_OVERLAY_CONFIG;
    const samples = sampleOrbit(satrec, now, periodMinutes(satrec), cfg.samples);
    const color = Cesium.Color.fromCssColorString(cfg.pathColor);
    if (samples.length >= 2) {
      const flat: number[] = [];
      for (const s of samples) flat.push(s.lon, s.lat, s.height);
      this.orbit.path = this.viewer.entities.add({
        polyline: {
          positions: Cesium.Cartesian3.fromDegreesArrayHeights(flat),
          width: cfg.pathWidth,
          material: color,
          arcType: Cesium.ArcType.NONE,
        },
        show: this.orbitOverlay.showPath(),
      });
      this.orbit.path._sentinelOrbit = true;
      const groundColor = Cesium.Color.fromCssColorString(cfg.groundColor).withAlpha(cfg.groundAlpha);
      for (const run of splitAtAntimeridian(samples)) {
        const ground: number[] = [];
        for (const s of run) ground.push(s.lon, s.lat);
        const g = this.viewer.entities.add({
          polyline: {
            positions: Cesium.Cartesian3.fromDegreesArray(ground),
            width: cfg.groundWidth,
            material: groundColor,
            clampToGround: true,
          },
          show: this.orbitOverlay.showGroundTrack(),
        });
        g._sentinelOrbit = true;
        this.orbit.ground.push(g);
      }
    }
    const radius = footprintRadiusM(pos.height);
    if (radius > 0) {
      const footprintPos = new Cesium.ConstantPositionProperty(Cesium.Cartesian3.fromDegrees(pos.lon, pos.lat, 0));
      this.orbit.footprint = this.viewer.entities.add({
        position: footprintPos,
        ellipse: {
          semiMajorAxis: radius,
          semiMinorAxis: radius,
          height: 0,
          material: Cesium.Color.fromCssColorString(cfg.footprintColor).withAlpha(cfg.footprintAlpha),
          outline: true,
          outlineColor: Cesium.Color.fromCssColorString(cfg.footprintColor).withAlpha(cfg.footprintOutlineAlpha),
          granularity: radius > 2_000_000 ? Cesium.Math.toRadians(0.5) : Cesium.Math.RADIANS_PER_DEGREE,
        },
        show: this.orbitOverlay.showFootprint(),
      });
      this.orbit.footprint._sentinelOrbit = true;
      this.orbit.footprint._posProp = footprintPos;
    }
    this.orbit.builtAtMs = now;
  }

  /** Move the footprint with the satellite; re-sample the path when it is stale. */
  private updateOrbitOverlay(id: string, pos: SatPosition, now: number): void {
    const Cesium = this.Cesium;
    if (this.orbit.footprint?._posProp) {
      this.orbit.footprint._posProp.setValue(Cesium.Cartesian3.fromDegrees(pos.lon, pos.lat, 0));
      const radius = footprintRadiusM(pos.height);
      if (radius > 0 && this.orbit.footprint.ellipse) {
        this.orbit.footprint.ellipse.semiMajorAxis = radius;
        this.orbit.footprint.ellipse.semiMinorAxis = radius;
      }
    }
    if (now - this.orbit.builtAtMs >= ORBIT_OVERLAY_CONFIG.rebuildMs) {
      const flags = { path: this.orbitOverlay.showPath(), ground: this.orbitOverlay.showGroundTrack(), footprint: this.orbitOverlay.showFootprint() };
      this.removeOrbitEntities();
      this.orbit.line1 = (this.entityMap.get(id)?._sentinelEntity?.platformData?.['tle'] as { line1?: string } | undefined)?.line1 ?? this.orbit.line1;
      this.buildOrbitOverlay(id, pos, now);
      if (this.orbit.path) this.orbit.path.show = flags.path;
      for (const g of this.orbit.ground) g.show = flags.ground;
      if (this.orbit.footprint) this.orbit.footprint.show = flags.footprint;
    }
  }

  private removeOrbitEntities(): void {
    if (!this.viewer || this.viewer.isDestroyed()) {
      this.orbit.path = null;
      this.orbit.ground = [];
      this.orbit.footprint = null;
      return;
    }
    if (this.orbit.path) this.viewer.entities.remove(this.orbit.path);
    for (const g of this.orbit.ground) this.viewer.entities.remove(g);
    if (this.orbit.footprint) this.viewer.entities.remove(this.orbit.footprint);
    this.orbit.path = null;
    this.orbit.ground = [];
    this.orbit.footprint = null;
  }

  /** Draws the loaded path as dashed runs at altitude; redraws only when it changes. */
  private syncFlightPath(state: FlightPathState): void {
    if (!this.viewer || this.viewer.isDestroyed()) return;
    const path = state.path;
    if (!path || path.points.length < 2) {
      this.clearFlightPath();
      return;
    }
    const key = `${path.hex}:${path.days}:${path.points.length}:${path.points[path.points.length - 1].ts}`;
    if (key === this.flightPathKey) return;
    this.clearFlightPath();
    const Cesium = this.Cesium;
    const cfg = FLIGHT_PATH_CONFIG;
    const material = new Cesium.PolylineDashMaterialProperty({
      color: Cesium.Color.fromCssColorString(cfg.color).withAlpha(cfg.alpha),
      dashLength: cfg.dashLength,
    });
    for (const run of pathRuns(path.points)) {
      const flat: number[] = [];
      for (const v of run) flat.push(v.lon, v.lat, v.height);
      const line = this.viewer.entities.add({
        polyline: { positions: Cesium.Cartesian3.fromDegreesArrayHeights(flat), width: cfg.width, material, arcType: Cesium.ArcType.NONE },
      });
      line._sentinelFlightPath = true;
      this.flightPathLines.push(line);
    }
    this.flightPathKey = key;
    this.scheduleRender();
  }

  private clearFlightPath(): void {
    if (this.flightPathLines.length === 0 && !this.flightPathKey) return;
    if (this.viewer && !this.viewer.isDestroyed()) {
      for (const line of this.flightPathLines) this.viewer.entities.remove(line);
    }
    this.flightPathLines = [];
    this.flightPathKey = '';
    this.scheduleRender();
  }

  private clearOrbitOverlay(): void {
    if (!this.orbit.forId && !this.orbit.path && !this.orbit.footprint && this.orbit.ground.length === 0) return;
    this.removeOrbitEntities();
    this.orbit.forId = null;
    this.orbit.builtAtMs = 0;
    this.orbit.line1 = '';
    this.scheduleRender();
  }

  private removeCesiumEntity(entityId: string): void {
    this.emplacementEntities.delete(entityId);
    this.deadReckoning.remove(entityId);
    this.satellites.remove(entityId);
    if (entityId === this.orbit.forId) this.clearOrbitOverlay();
    const cesiumEntity = this.entityMap.get(entityId);
    if (cesiumEntity && this.viewer) {
      if (entityId === this.dropLine.forId) this.clearDropLine();
      (cesiumEntity.entityCollection ?? this.viewer.entities).remove(cesiumEntity);
      this.entityMap.delete(entityId);
      this.trackTrails.delete(entityId);
      this.scheduleRender();
    }
  }

  // --- Collections and clustering --------------------------------------------

  /**
   * Aircraft, vessels and vehicles each live in their own data source so
   * Cesium can cluster them when their pins crowd together; everything else
   * stays in the viewer's default collection.
   */
  private setupClusterSources(): void {
    const Cesium = this.Cesium;
    for (const type of CLUSTERED_TYPES) {
      const source = new Cesium.CustomDataSource(`sentinel-${type.toLowerCase()}`);
      source.clustering.enabled = true;
      source.clustering.pixelRange = CLUSTER_CONFIG.pixelRange;
      source.clustering.minimumClusterSize = CLUSTER_CONFIG.minimumClusterSize;
      source.clustering.clusterEvent.addEventListener((clustered: any[], cluster: any) => {
        cluster.label.show = false;
        cluster.point.show = false;
        cluster.billboard.show = true;
        cluster.billboard.image = this.clusterImage(type, clustered.length);
        cluster.billboard.scale = 0.5;
        cluster.billboard.verticalOrigin = Cesium.VerticalOrigin.CENTER;
        cluster.billboard.disableDepthTestDistance = Number.POSITIVE_INFINITY;
        cluster.billboard.id = { _sentinelCluster: clustered };
      });
      this.viewer.dataSources.add(source);
      this.clusterSources.set(type, source);
    }
    // Vessel counts for views too large to show vessels (see syncDensity).
    this.densitySource = new Cesium.CustomDataSource('sentinel-vessel-density');
    this.viewer.dataSources.add(this.densitySource);
    // Clustered labels need their glyphs brought in line after each frame;
    // see syncLabelGlyphClusterShow.
    this.viewer.scene.postRender.addEventListener(() => {
      let changed = false;
      for (const source of this.clusterSources.values()) {
        changed = syncLabelGlyphClusterShow(source.clustering._labelCollection) || changed;
      }
      if (changed) this.scheduleRender();
    });
  }

  private collectionFor(entityType: string): any {
    const group = clusterGroupFor(entityType);
    return (group && this.clusterSources.get(group)?.entities) ?? this.viewer.entities;
  }

  private suspendCollectionEvents(): void {
    this.viewer.entities.suspendEvents();
    for (const source of this.clusterSources.values()) source.entities.suspendEvents();
  }

  private resumeCollectionEvents(): void {
    this.viewer.entities.resumeEvents();
    for (const source of this.clusterSources.values()) source.entities.resumeEvents();
  }

  /** The disc for a cluster of `count` pins of one type, drawn once per type and count text. */
  private clusterImage(type: EntityType, count: number): string {
    const spec = clusterDiscSpec(count);
    const key = `${type}:${spec.text}`;
    let image = this.clusterImageCache.get(key);
    if (!image) {
      this.clusterCanvas ??= document.createElement('canvas');
      const fill = ENTITY_TYPE_PIN_COLORS[type] ?? ENTITY_TYPE_PIN_COLORS[EntityType.UNKNOWN];
      image = drawClusterDisc(this.clusterCanvas, spec, fill, isLightColor(fill) ? '#0a0e17' : '#ffffff');
      this.clusterImageCache.set(key, image);
    }
    return image;
  }

  // --- Vessel density -------------------------------------------------------

  /**
   * The aisstream feed counts vessels instead of storing them while the
   * viewed area is over its cap, and sends the counts as density grids.
   */
  private subscribeToDensity(): void {
    const sub = this.wsService.density$.subscribe((grid) => {
      if (grid.entityType !== EntityType.VESSEL) return;
      this.density = { grid, receivedAt: Date.now() };
      this.ngZone.runOutsideAngular(() => this.syncDensity());
    });
    this.subscriptions.add(sub);
    // A grid goes stale when the feed stops sending; drop it without waiting for a camera move.
    this.densityTimer = setInterval(() => {
      if (this.densityShown) this.ngZone.runOutsideAngular(() => this.syncDensity());
    }, DENSITY_OVERLAY_CONFIG.recheckMs);
  }

  /**
   * Draws the density grid as counted discs while it applies to the view
   * (fresh, view over the feed's cap, Vessels layer on), hiding individual
   * vessels meanwhile so the two kinds of disc never overlap; clears it and
   * shows the vessels again otherwise.
   */
  private syncDensity(): void {
    if (!this.viewer || !this.Cesium || !this.densitySource) return;
    const Cesium = this.Cesium;
    const view = this.mapViewport.bounds();
    const vesselsOn = this.layers.find((l) => l.entityType === EntityType.VESSEL)?.visible ?? true;
    const d = this.density;
    const show = vesselsOn && !!d && densityApplies(d.grid, d.receivedAt, view, Date.now());
    if (!show && !this.densityShown) return;

    const entities = this.densitySource.entities;
    entities.suspendEvents();
    entities.removeAll();
    if (show && d && view) {
      for (const b of bucketCells(d.grid.cells, view, bucketDegrees(view, d.grid.cellDegrees))) {
        const disc = entities.add({
          position: Cesium.Cartesian3.fromDegrees(b.lon, b.lat),
          billboard: {
            image: this.densityImage(b.count),
            scale: 0.5,
            verticalOrigin: Cesium.VerticalOrigin.CENTER,
            disableDepthTestDistance: Number.POSITIVE_INFINITY,
          },
        });
        disc._sentinelDensity = { lat: b.lat, lon: b.lon };
      }
    }
    entities.resumeEvents();
    const vessels = this.clusterSources.get(EntityType.VESSEL);
    if (vessels) vessels.show = !show;
    this.densityShown = show;
    this.scheduleRender();
  }

  /** The disc for a density bucket, drawn once per size and label. */
  private densityImage(count: number): string {
    const spec = { size: densityDiscSize(count), text: densityLabel(count) };
    const key = `density:${spec.size}:${spec.text}`;
    let image = this.clusterImageCache.get(key);
    if (!image) {
      this.clusterCanvas ??= document.createElement('canvas');
      image = drawClusterDisc(this.clusterCanvas, spec, DENSITY_OVERLAY_CONFIG.fill, DENSITY_OVERLAY_CONFIG.text);
      this.clusterImageCache.set(key, image);
    }
    return image;
  }

  /** Zoom in on a cluster until its members come apart. */
  private flyToCluster(members: any[]): void {
    const Cesium = this.Cesium;
    const now = Cesium.JulianDate.now();
    const points = members
      .map((m) => m.position?.getValue(now))
      .filter((p: any) => Cesium.defined(p));
    if (points.length === 0) return;
    const sphere = Cesium.BoundingSphere.fromPoints(points);
    const range = Math.max(sphere.radius * 3, 5_000);
    this.viewer.camera.flyToBoundingSphere(sphere, {
      duration: 0.8,
      offset: new Cesium.HeadingPitchRange(this.viewer.camera.heading, this.viewer.camera.pitch, range),
    });
  }

  // --- Geofences ------------------------------------------------------------------

  /** Where a ring's anchor track is drawn now: its live map position, else its last stored fix. */
  private geofenceAnchorPosition(entityId: string): LngLat | null {
    const live = this.entityMap.get(entityId)?._sentinelEntity as Entity | undefined;
    const geo = live ? this.displayedGeoPoint(live, this.wsService.serverNow()) : null;
    if (geo) return [geo.lon, geo.lat];
    const stored = this.geofences.anchors().get(entityId);
    return stored?.position ? [stored.position.longitude, stored.position.latitude] : null;
  }

  /** Where a mobile emplacement is now: its SENSOR entity's last fix, while it is on the stream. */
  private emplacementPosition(emplacementId: string): { lon: number; lat: number; heading: number | null } | null {
    for (const e of this.emplacementEntities.values()) {
      if (e.position && e.ageoutState !== 'AGED_OUT' && emplacementIdOf(e) === emplacementId) {
        return { lon: e.position.longitude, lat: e.position.latitude, heading: e.heading ?? null };
      }
    }
    return null;
  }

  /**
   * Keep the loaded emplacement ids in step with the list. When they change,
   * an entity drawn as an ordinary one that now stands for a loaded emplacement
   * goes to the emplacement layer, and one left to the layer whose emplacement
   * is no longer loaded is drawn as an ordinary entity again.
   */
  private syncLoadedEmplacements(): void {
    const ids = new Set(this.emplacements.emplacements().map((e) => e.id));
    if (ids.size === this.loadedEmplacementIds.size && [...ids].every((id) => this.loadedEmplacementIds.has(id))) return;
    this.loadedEmplacementIds = ids;
    for (const [id, cesiumEntity] of [...this.entityMap]) {
      const entity = cesiumEntity._sentinelEntity as Entity | undefined;
      if (entity && emplacementDrawingOf(entity, ids)) {
        this.removeCesiumEntity(id);
        this.emplacementEntities.set(id, entity);
      }
    }
    for (const [id, entity] of [...this.emplacementEntities]) {
      if (!emplacementDrawingOf(entity, ids)) {
        this.emplacementEntities.delete(id);
        this.addOrUpdateCesiumEntity(entity);
      }
    }
    this.scheduleRender();
  }

  /** Redraw the emplacement layer from the service's state, drawing nothing while its layer is hidden. */
  private resyncEmplacements(): void {
    const list = this.emplacements.emplacements();
    const selectedId = this.emplacements.selectedId();
    const draft = this.emplacements.draft();
    const coverage = this.emplacements.showCoverage();
    const visible = this.layerVisibility.isVisible(EMPLACEMENTS_LAYER);
    this.ngZone.runOutsideAngular(() =>
      this.emplacementLayer?.sync(visible ? list : [], selectedId, visible ? draft : null, coverage, (id) => this.emplacementPosition(id)),
    );
  }

  /** Redraw the geofence layer from the service's current state. */
  private resyncGeofences(): void {
    this.ngZone.runOutsideAngular(() => this.geofenceLayer?.sync(this.geofences.zones(), this.geofences.selectedId(), (id) => this.geofenceAnchorPosition(id), this.geofences.draft()));
  }

  /** The Geofences map control: open the panel and start a new zone in one step. */
  openGeofences(): void {
    this.router.navigateByUrl('/geofences');
    if (this.geofences.canWrite()) this.geofences.startDrawingRequested.next();
  }

  /** Enter draw mode with the last-used colour; redrawing a zone starts from its shape and colour. */
  startDrawing(shape: GeofenceShape = 'polygon'): void {
    this.selectedEntity.set(null);
    const redraw = this.geofences.redrawing();
    if (redraw) {
      const radius = redraw.config.circle?.radiusM ?? redraw.config.ring?.radiusM ?? 5000;
      this.drawSession.set(newSession(redraw.config.shape, redraw.config.color, radius));
      return;
    }
    this.geofences.select(null);
    this.drawSession.set(newSession(shape, this.geofences.lastColor()));
  }

  setDrawShape(shape: GeofenceShape): void {
    const s = this.drawSession();
    if (s) this.drawSession.set(newSession(shape, s.color, s.radiusM));
  }

  setDrawColor(color: string): void {
    const s = this.drawSession();
    if (s) this.drawSession.set({ ...s, color });
  }

  setDrawRadius(radiusM: number): void {
    const s = this.drawSession();
    if (s) this.drawSession.set(setRadius(s, radiusM));
  }

  cancelDrawing(): void {
    this.drawSession.set(null);
    this.geofences.redrawing.set(null);
  }

  /** Finish turns the shape into a draft the panel's editor opens, or replaces a redrawn zone's shape. */
  finishDrawing(): void {
    const s = this.drawSession();
    if (!s || !canFinish(s).ok) return;
    this.geofences.rememberColor(s.color);
    const redraw = this.geofences.redrawing();
    if (redraw) {
      const config: GeofenceConfig = { ...redraw.config, ...finishShape(s) };
      if (config.shape !== 'circle') delete config.circle;
      if (config.shape !== 'ring') delete config.ring;
      if (config.shape === 'ring') delete config.polygon;
      this.geofences.redrawing.set(null);
      this.geofences.update(redraw.id, { config }).subscribe({ error: () => undefined });
      this.geofences.select(redraw.id);
    } else {
      this.geofences.setDraft(draftFromSession(s));
    }
    this.drawSession.set(null);
    this.router.navigateByUrl('/geofences');
  }

  /** A click while drawing: a vertex, the radius, or the anchor track for a ring. */
  private drawClick(position: any, pickedObject: any): void {
    const session = this.drawSession();
    if (!session) return;
    if (session.shape === 'ring' && !session.anchor) {
      const entity = this.trackAt(position, pickedObject);
      if (!entity) return;
      const geo = this.displayedGeoPoint(entity, this.wsService.serverNow());
      if (!geo) return;
      this.ngZone.run(() => this.drawSession.set(setAnchor(session, { entityId: entity.id, entityName: entity.name, position: [geo.lon, geo.lat] })));
      return;
    }
    const p = this.pickLngLat(position);
    if (!p) return;
    this.ngZone.run(() => this.drawSession.set(addPoint(session, p)));
  }

  /** The track under a click, looking a few pixels around it: track icons are small targets. */
  private trackAt(position: any, pickedObject: any): Entity | undefined {
    const direct = pickedObject?.id?._sentinelEntity as Entity | undefined;
    if (direct) return direct;
    const around = this.viewer.scene.drillPick(position, 8, ANCHOR_PICK_PX, ANCHOR_PICK_PX) as any[];
    return around.map((p) => p?.id?._sentinelEntity as Entity | undefined).find((e) => !!e);
  }

  /** The globe position under a screen point, or null when the point misses the globe. */
  private pickLngLat(screen: any): LngLat | null {
    const Cesium = this.Cesium;
    const ray = this.viewer.camera.getPickRay(screen);
    const cartesian = (ray && this.viewer.scene.globe.pick(ray, this.viewer.scene)) || this.viewer.camera.pickEllipsoid(screen, this.viewer.scene.globe.ellipsoid);
    if (!cartesian) return null;
    const carto = Cesium.Cartographic.fromCartesian(cartesian);
    return [Cesium.Math.toDegrees(carto.longitude), Cesium.Math.toDegrees(carto.latitude)];
  }

  // --- Satellite altitude -------------------------------------------------------

  private satelliteLabel(name: string, heightM: number): string {
    return `${name} · ${formatAltitude(heightM)}`;
  }

  /** Show the drop line under the selected aircraft, drone or satellite, or none. */
  private syncDropLine(selected: Entity | null): void {
    if (!this.viewer || !this.Cesium) return;
    if (!selected || !showsDropLine(selected)) {
      this.clearDropLine();
      return;
    }
    if (this.dropLine.forId === selected.id) return;
    this.clearDropLine();
    this.dropLine.forId = selected.id;
    const pos = this.displayedGeoPoint(selected, this.wsService.serverNow());
    if (pos) this.moveDropLine(pos);
  }

  /** Where an entity is drawn right now: propagated, dead-reckoned, or its last fix. */
  private displayedGeoPoint(entity: Entity, now: number): GeoPoint | null {
    const sat = this.satellites.has(entity.id) ? this.satellites.positionOf(entity.id, now) : null;
    if (sat) return sat;
    const dr = this.deadReckoning.positionOf(entity.id, now);
    if (dr) return { lat: dr.lat, lon: dr.lon, height: dr.alt };
    const p = entity.position;
    return p ? { lat: p.latitude, lon: p.longitude, height: p.altitude ?? 0 } : null;
  }

  private moveDropLine(pos: GeoPoint): void {
    const Cesium = this.Cesium;
    const geometry = dropLinePoints(pos);
    this.dropLine.positions = [
      Cesium.Cartesian3.fromDegrees(geometry.top.lon, geometry.top.lat, geometry.top.height),
      Cesium.Cartesian3.fromDegrees(geometry.ground.lon, geometry.ground.lat, geometry.ground.height),
    ];
    const mid = Cesium.Cartesian3.fromDegrees(geometry.mid.lon, geometry.mid.lat, geometry.mid.height);
    if (!this.dropLine.line) {
      const color = Cesium.Color.fromCssColorString(DROP_LINE_CONFIG.color);
      this.dropLine.line = this.viewer.entities.add({
        polyline: {
          positions: new Cesium.CallbackProperty(() => this.dropLine.positions, false),
          width: DROP_LINE_CONFIG.width,
          material: new Cesium.PolylineDashMaterialProperty({ color, dashLength: DROP_LINE_CONFIG.dashLength }),
        },
      });
      this.dropLine.line._sentinelDropLine = true;
      this.dropLine.label = this.viewer.entities.add({
        position: new Cesium.ConstantPositionProperty(mid),
        label: {
          text: geometry.label,
          font: DROP_LINE_CONFIG.labelFont,
          fillColor: color,
          outlineColor: Cesium.Color.BLACK,
          outlineWidth: 2,
          style: Cesium.LabelStyle.FILL_AND_OUTLINE,
          showBackground: true,
          backgroundColor: Cesium.Color.fromCssColorString('#0a0e17').withAlpha(0.7),
          backgroundPadding: new Cesium.Cartesian2(5, 3),
          pixelOffset: new Cesium.Cartesian2(8, 0),
          horizontalOrigin: Cesium.HorizontalOrigin.LEFT,
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
        },
      });
      this.dropLine.label._sentinelDropLine = true;
      this.dropLine.labelText = geometry.label;
    } else {
      this.dropLine.label.position.setValue(mid);
      if (this.dropLine.labelText !== geometry.label) {
        this.dropLine.labelText = geometry.label;
        this.dropLine.label.label.text = geometry.label;
      }
    }
    this.scheduleRender();
  }

  private clearDropLine(): void {
    if (this.dropLine.line) this.viewer?.entities.remove(this.dropLine.line);
    if (this.dropLine.label) this.viewer?.entities.remove(this.dropLine.label);
    this.dropLine = { forId: null, line: null, label: null, positions: [], labelText: '' };
    this.scheduleRender();
  }

  private updateTrackTrail(entity: Entity): void {
    if (!entity.position) return;

    let trail = this.trackTrails.get(entity.id);
    if (!trail) {
      trail = new CircularBuffer<{ lat: number; lon: number; alt: number }>(TRACK_TRAIL_CONFIG.maxPoints);
      this.trackTrails.set(entity.id, trail);
    }

    trail.push({
      lat: entity.position.latitude,
      lon: entity.position.longitude,
      alt: entity.position.altitude ?? 0,
    });
  }

  /**
   * Wait until an element has non-zero clientWidth and clientHeight.
   * Uses a ResizeObserver for efficiency, with a rAF-polling fallback.
   */
  private waitForLayout(el: HTMLElement): Promise<void> {
    if (el.clientWidth > 0 && el.clientHeight > 0) {
      return Promise.resolve();
    }

    return new Promise<void>((resolve) => {
      // Prefer ResizeObserver — fires as soon as the element gains dimensions.
      if (typeof ResizeObserver !== 'undefined') {
        const ro = new ResizeObserver((entries) => {
          for (const entry of entries) {
            const { width, height } = entry.contentRect;
            if (width > 0 && height > 0) {
              ro.disconnect();
              resolve();
              return;
            }
          }
        });
        ro.observe(el);
      } else {
        // Fallback: poll via rAF
        const check = () => {
          if (el.clientWidth > 0 && el.clientHeight > 0) {
            resolve();
          } else {
            requestAnimationFrame(check);
          }
        };
        requestAnimationFrame(check);
      }
    });
  }

  // --- Public methods for template ---

  toggleLayer(layer: LayerConfig): void {
    // Show/hide all entities of this type
    this.entityMap.forEach((cesiumEntity) => {
      const sentinelEntity = cesiumEntity._sentinelEntity as Entity;
      if (sentinelEntity.entityType === layer.entityType) {
        cesiumEntity.show = layer.visible && this.layerVisibility.isVisible(sentinelEntity);
      }
    });
    if (layer.entityType === EntityType.VESSEL) this.syncDensity();
    this.scheduleRender();
  }

  /** Re-applies the user's hidden layer set (feed id / source) to every entity. */
  private applyLayerVisibility(): void {
    if (this.entityMap.size === 0) return;
    this.entityMap.forEach((cesiumEntity) => {
      const sentinelEntity = cesiumEntity._sentinelEntity as Entity;
      const typeLayer = this.layers.find((l) => l.entityType === sentinelEntity.entityType);
      cesiumEntity.show = (typeLayer?.visible ?? true) && this.layerVisibility.isVisible(sentinelEntity);
    });
    this.scheduleRender();
  }

  resetView(): void {
    if (!this.viewer || !this.Cesium) return;

    const Cesium = this.Cesium;
    this.viewer.camera.flyTo({
      destination: Cesium.Cartesian3.fromDegrees(
        DEFAULT_CAMERA_POSITION.longitude,
        DEFAULT_CAMERA_POSITION.latitude,
        DEFAULT_CAMERA_POSITION.height,
      ),
      duration: 1.5,
    });
  }

  toggleFullscreen(): void {
    if (!document.fullscreenElement) {
      document.documentElement.requestFullscreen().catch(() => {});
    } else {
      document.exitFullscreen().catch(() => {});
    }
  }

  flyToEntity(entity: Entity): void {
    if (!this.viewer || !this.Cesium || !entity.position) return;

    const Cesium = this.Cesium;
    this.viewer.camera.flyTo({
      destination: Cesium.Cartesian3.fromDegrees(
        entity.position.longitude,
        entity.position.latitude,
        50000,
      ),
      duration: 1.0,
    });
  }

  clearAllEntities(): void {
    if (!confirm('Clear all entities? This will permanently remove all tracked entities from the system.')) {
      return;
    }

    this.entityService.deleteAllEntities().subscribe({
      next: (result) => {
        // Remove tracked entities from the Cesium viewer (preserve user location dot)
        if (this.viewer) {
          this.clearDropLine();
          this.clearFlightPath();
          for (const cesiumEntity of this.entityMap.values()) {
            (cesiumEntity.entityCollection ?? this.viewer.entities).remove(cesiumEntity);
          }
        }
        this.entityMap.clear();
        this.emplacementEntities.clear();
        this.trackTrails.clear();
        this.deadReckoning.clear();
        this.satellites.clear();
        this.selectedEntity.set(null);
      },
      error: (err) => {
        console.error('Failed to clear entities:', err);
      },
    });
  }

  closeEntityPopup(): void {
    this.selectedEntity.set(null);
  }

  private fetchAgeoutConfigs(): void {
    this.http.get<Array<{ sourceType: string | null; staleThresholdMs: number }>>('/api/v1/entities/ageout-config')
      .subscribe({
        next: (configs) => { this.ageoutConfigs = configs; },
        error: () => { /* use cached or defaults */ },
      });
  }

  private getStaleThresholdForSource(source: string): number {
    const specific = this.ageoutConfigs.find((c) => c.sourceType === source);
    if (specific) return specific.staleThresholdMs;
    const global = this.ageoutConfigs.find((c) => c.sourceType === null);
    return global?.staleThresholdMs ?? 300_000;
  }

  private isEntityStale(entity: Entity): boolean {
    if (entity.ageoutState === 'STALE') return true;
    if (entity.ageoutState === 'AGED_OUT') return true;
    if (entity.lastSeenAt && entity.source) {
      const elapsed = Date.now() - new Date(entity.lastSeenAt).getTime();
      return elapsed > this.getStaleThresholdForSource(entity.source);
    }
    return false;
  }

  /** The pin colour for one entity: vessels by ship type category, everything else by type. */
  private entityColor(entity: Entity): any {
    if (entity.entityType !== EntityType.VESSEL || !this.Cesium) return this.getCesiumColor(entity.entityType);
    const css = vesselColor(entity);
    let cached = this.cesiumColorCache.get(css);
    if (!cached) {
      cached = this.Cesium.Color.fromCssColorString(css);
      this.cesiumColorCache.set(css, cached);
    }
    return cached;
  }

  private getCesiumColor(entityType: EntityType): any {
    let cached = this.cesiumColorCache.get(entityType);
    if (!cached && this.Cesium) {
      const color = ENTITY_TYPE_COLORS[entityType] ?? ENTITY_TYPE_COLORS[EntityType.UNKNOWN];
      cached = new this.Cesium.Color(color.red, color.green, color.blue, color.alpha);
      this.cesiumColorCache.set(entityType, cached);
    }
    return cached;
  }

  /** IntelSky reports only military and government aircraft; ADSB_LOL is the feed it replaced. */
  private isMilitaryAircraft(entity: Entity): boolean {
    return entity.entityType === EntityType.AIRCRAFT
      && (entity.source === EntitySource.INTELSKY || entity.source === EntitySource.ADSB_LOL);
  }

  private getBillboardImage(entity: Entity): string {
    const isMilAircraft = this.isMilitaryAircraft(entity);
    const cacheKey = isMilAircraft ? 'MIL_AIRCRAFT' : entity.entityType;

    if (!this.billboardImageCache.has(cacheKey)) {
      const svg = isMilAircraft
        ? MILITARY_AIRCRAFT_BILLBOARD_SVG
        : (ENTITY_TYPE_BILLBOARD_SVGS[entity.entityType] ?? ENTITY_TYPE_BILLBOARD_SVGS[EntityType.UNKNOWN]);
      this.billboardImageCache.set(cacheKey, svgToDataUrl(svg));
    }
    return this.billboardImageCache.get(cacheKey)!;
  }

  private refreshTrailDecimation(): void {
    if (!this.viewer || !this.Cesium) return;

    const cartographic = this.Cesium.Cartographic.fromCartesian(this.viewer.camera.position);
    const newAltitude = cartographic.height;

    const oldStride = this.getStride(this.currentCameraAltitude);
    const newStride = this.getStride(newAltitude);
    this.currentCameraAltitude = newAltitude;

    if (oldStride === newStride) return;

    this.entityMap.forEach((cesiumEntity, entityId) => {
      const trailBuffer = this.trackTrails.get(entityId);
      if (!trailBuffer || trailBuffer.length < 2 || !cesiumEntity.polyline) return;

      const decimated = decimateTrail(
        trailBuffer.toArray(),
        this.currentCameraAltitude,
        TRACK_TRAIL_CONFIG.decimation,
      );

      if (decimated.length >= 2) {
        cesiumEntity.polyline.positions = decimated.map((p: TrailPoint) =>
          this.Cesium.Cartesian3.fromDegrees(p.lon, p.lat, p.alt),
        );
      }
    });

    this.scheduleRender();
  }

  private getStride(altitude: number): number {
    const d = TRACK_TRAIL_CONFIG.decimation;
    if (altitude > d.HIGH_ALT_THRESHOLD) return d.HIGH_ALT_STRIDE;
    if (altitude > d.MID_ALT_THRESHOLD) return d.MID_ALT_STRIDE;
    return d.LOW_ALT_STRIDE;
  }

  private scheduleRender(): void {
    if (this.renderScheduled || !this.viewer) return;
    this.renderScheduled = true;
    requestAnimationFrame(() => {
      this.renderScheduled = false;
      if (this.viewer && !this.viewer.isDestroyed()) {
        this.viewer.scene.requestRender();
      }
    });
  }

  private subscribeToFlyTo(): void {
    const sub = this.locationService.flyTo$.subscribe((location) => {
      this.flyToLocation(location);
    });
    this.subscriptions.add(sub);
  }

  private flyToLocation(location: Location): void {
    if (!this.viewer || !this.Cesium) return;

    const Cesium = this.Cesium;

    this.ngZone.run(() => this.flyingTo.set(location.name));

    if (location.has3dTiles) {
      this.buildingsService.ensureEnabled();
    }

    const currentPos = Cesium.Cartographic.fromCartesian(this.viewer.camera.position);
    const targetPos = Cesium.Cartographic.fromDegrees(location.longitude, location.latitude);
    const distance = Cesium.Cartesian3.distance(
      Cesium.Cartesian3.fromRadians(currentPos.longitude, currentPos.latitude),
      Cesium.Cartesian3.fromRadians(targetPos.longitude, targetPos.latitude),
    );
    const duration = Math.min(3, Math.max(1, distance / 5_000_000));

    this.viewer.camera.flyTo({
      destination: Cesium.Cartesian3.fromDegrees(
        location.longitude,
        location.latitude,
        location.altitude,
      ),
      orientation: {
        heading: Cesium.Math.toRadians(location.heading),
        pitch: Cesium.Math.toRadians(location.pitch),
        roll: 0,
      },
      duration,
      complete: () => {
        this.ngZone.run(() => this.flyingTo.set(null));
      },
    });
  }

  private initUserLocation(): void {
    if (!this.viewer || !this.Cesium) return;

    const Cesium = this.Cesium;
    const saved = localStorage.getItem('sentinel-camera-position');

    if (saved) {
      try {
        const { lon, lat, alt, heading, pitch } = JSON.parse(saved);
        this.viewer.camera.setView({
          destination: Cesium.Cartesian3.fromDegrees(lon, lat, alt),
          orientation: { heading, pitch, roll: 0 },
        });
      } catch {
        // Invalid saved data — fall through to geolocation
      }
    }

    // Always try to place the blue dot, even if restoring camera from localStorage
    if (navigator.geolocation) {
      navigator.geolocation.getCurrentPosition(
        (pos) => {
          this.updateUserLocationMarker(pos.coords.longitude, pos.coords.latitude);

          // Only fly to user location on first visit (no saved camera)
          if (!saved) {
            this.viewer.camera.flyTo({
              destination: Cesium.Cartesian3.fromDegrees(
                pos.coords.longitude,
                pos.coords.latitude,
                50000,
              ),
              duration: 2,
            });
          }
          localStorage.setItem('sentinel-has-geolocated', 'true');
        },
        () => { /* permission denied — stay at default view */ },
      );
    }
  }

  private saveCameraPosition(): void {
    if (!this.viewer || !this.Cesium) return;

    try {
      const pos = this.viewer.camera.positionCartographic;
      localStorage.setItem('sentinel-camera-position', JSON.stringify({
        lon: this.Cesium.Math.toDegrees(pos.longitude),
        lat: this.Cesium.Math.toDegrees(pos.latitude),
        alt: pos.height,
        heading: this.viewer.camera.heading,
        pitch: this.viewer.camera.pitch,
      }));
    } catch {
      // Cartographic conversion can fail in edge cases
    }
  }

  goToMyLocation(): void {
    if (!this.viewer || !this.Cesium || !navigator.geolocation) return;

    const Cesium = this.Cesium;
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        this.updateUserLocationMarker(pos.coords.longitude, pos.coords.latitude);
        this.viewer.camera.flyTo({
          destination: Cesium.Cartesian3.fromDegrees(
            pos.coords.longitude,
            pos.coords.latitude,
            50000,
          ),
          duration: 2,
        });
      },
      () => { /* permission denied */ },
    );
  }

  private updateUserLocationMarker(lon: number, lat: number): void {
    if (!this.viewer || !this.Cesium) return;

    const Cesium = this.Cesium;
    const position = Cesium.Cartesian3.fromDegrees(lon, lat);

    if (this.userLocationEntity) {
      this.userLocationEntity.position = position;
    } else {
      // Blue dot SVG with pulse ring — similar to Apple Maps
      const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64">
        <circle cx="32" cy="32" r="28" fill="rgba(59,130,246,0.12)" stroke="rgba(59,130,246,0.3)" stroke-width="1"/>
        <circle cx="32" cy="32" r="18" fill="rgba(59,130,246,0.2)" stroke="rgba(59,130,246,0.4)" stroke-width="1"/>
        <circle cx="32" cy="32" r="9" fill="#3b82f6" stroke="white" stroke-width="2.5"/>
      </svg>`;
      const dataUrl = 'data:image/svg+xml;base64,' + btoa(svg);

      this.userLocationEntity = this.viewer.entities.add({
        position,
        billboard: {
          image: dataUrl,
          scale: 0.7,
          heightReference: Cesium.HeightReference.CLAMP_TO_GROUND,
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
          verticalOrigin: Cesium.VerticalOrigin.CENTER,
        },
      });
    }

    this.scheduleRender();
  }
}
