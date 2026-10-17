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
import { Subscription, debounceTime, Subject, throttleTime, bufferTime, filter } from 'rxjs';
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
} from './cesium-config';
import { CircularBuffer, decimateTrail, TrailPoint } from './trail-utils';
import { isGlobalBounds, rectangleToBounds } from './viewport-bounds';
import { DeadReckoningTracker } from './dead-reckoning';
import { SatelliteTracker, periodMinutes, type SatPosition } from './satellite-tracker';
import { footprintRadiusM, sampleOrbit, splitAtAntimeridian } from './orbit-geometry';
import { OrbitOverlayService } from '../../core/services/orbit-overlay.service';

/** Dead-reckoning frame cap: ~30 fps is plenty for gliding markers. */
/** Whether a #rrggbb colour is light enough to need dark text on it. */
const DR_FRAME_INTERVAL_MS = 33;
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
  imports: [CommonModule, FormsModule, EntityDetailPanelComponent, IconComponent],
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
  private drFrame: number | null = null;
  private drLastTickMs = 0;
  private drScratch: any = null;
  private themePostProcessStage: any = null;

  /** Cached ageout configs fetched from backend, refreshed every 60s */
  private ageoutConfigs: Array<{ sourceType: string | null; staleThresholdMs: number }> = [];
  private ageoutConfigTimer: ReturnType<typeof setInterval> | null = null;

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
    this.initUserLocation();

    // Report the initial viewport: a saved camera position is restored with
    // setView, which does not fire moveEnd. A later flyTo (geolocation)
    // fires moveEnd and refreshes it.
    this.sendViewportBounds();
  }

  ngOnDestroy(): void {
    this.clearOrbitOverlay();
    if (this.ageoutConfigTimer) clearInterval(this.ageoutConfigTimer);
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

      if (Cesium.defined(pickedObject) && pickedObject.id?._sentinelOrbit) {
        return; // a click on the selected satellite's own overlay keeps the selection
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
        this.viewer.entities.suspendEvents();
        try {
          entities.forEach((entity, id) => {
            if (!this.entityMap.has(id)) {
              this.addOrUpdateCesiumEntity(entity);
            }
          });
        } finally {
          this.viewer.entities.resumeEvents();
        }
        this.scheduleRender();
      });
    });
    this.subscriptions.add(entityStateSub);

    // Subscribe to entity evictions — remove stale entities from the globe
    const evictionSub = this.entityService.entityEvictions$.subscribe((ids) => {
      this.ngZone.runOutsideAngular(() => {
        if (!this.viewer) return;
        this.viewer.entities.suspendEvents();
        try {
          for (const id of ids) {
            this.removeCesiumEntity(id);
          }
        } finally {
          this.viewer.entities.resumeEvents();
        }
        this.scheduleRender();
      });
    });
    this.subscriptions.add(evictionSub);
  }

  private processCesiumBatch(events: EntityEvent[]): void {
    if (!this.viewer) return;

    console.debug(`[Map] Processing batch of ${events.length} entity events`);

    this.viewer.entities.suspendEvents();
    try {
      for (const event of events) {
        this.handleEntityEvent(event);
      }
    } catch (err) {
      console.error('[Map] Error processing entity batch:', err);
    } finally {
      this.viewer.entities.resumeEvents();
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

    const cesiumColor = this.getCesiumColor(entity.entityType);
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

    const shouldRotate = HEADING_ROTATED_TYPES.has(entity.entityType);
    const rotation = shouldRotate && entity.heading != null
      ? -Cesium.Math.toRadians(entity.heading)
      : 0;

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

    if (existing) {
      // Update existing entity
      existing.show = shown;
      existing._posProp.setValue(displayPosition);
      existing.label.text = entity.name;
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
      const cesiumEntity = this.viewer.entities.add({
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
          text: entity.name,
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
      this.entityMap.set(entity.id, cesiumEntity);
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

  private clearOrbitOverlay(): void {
    if (!this.orbit.forId && !this.orbit.path && !this.orbit.footprint && this.orbit.ground.length === 0) return;
    this.removeOrbitEntities();
    this.orbit.forId = null;
    this.orbit.builtAtMs = 0;
    this.orbit.line1 = '';
    this.scheduleRender();
  }

  private removeCesiumEntity(entityId: string): void {
    this.deadReckoning.remove(entityId);
    this.satellites.remove(entityId);
    if (entityId === this.orbit.forId) this.clearOrbitOverlay();
    const cesiumEntity = this.entityMap.get(entityId);
    if (cesiumEntity && this.viewer) {
      this.viewer.entities.remove(cesiumEntity);
      this.entityMap.delete(entityId);
      this.trackTrails.delete(entityId);
      this.scheduleRender();
    }
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
          for (const cesiumEntity of this.entityMap.values()) {
            this.viewer.entities.remove(cesiumEntity);
          }
        }
        this.entityMap.clear();
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

  private getCesiumColor(entityType: EntityType): any {
    let cached = this.cesiumColorCache.get(entityType);
    if (!cached && this.Cesium) {
      const color = ENTITY_TYPE_COLORS[entityType] ?? ENTITY_TYPE_COLORS[EntityType.UNKNOWN];
      cached = new this.Cesium.Color(color.red, color.green, color.blue, color.alpha);
      this.cesiumColorCache.set(entityType, cached);
    }
    return cached;
  }

  private isMilitaryAircraft(entity: Entity): boolean {
    return entity.entityType === EntityType.AIRCRAFT
      && entity.source === EntitySource.ADSB_LOL;
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
