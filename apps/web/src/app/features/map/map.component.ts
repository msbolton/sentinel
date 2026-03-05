import {
  Component,
  AfterViewInit,
  OnDestroy,
  ViewChild,
  ElementRef,
  signal,
  NgZone,
  ChangeDetectionStrategy,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Subscription, debounceTime, Subject, throttleTime } from 'rxjs';
import {
  Entity,
  EntityType,
  EntityEvent,
} from '../../shared/models/entity.model';
import { EntityService } from '../../core/services/entity.service';
import { WebSocketService } from '../../core/services/websocket.service';
import { ThemeService, ThemePreset } from '../../core/services/theme.service';
import {
  configureCesium,
  CESIUM_VIEWER_OPTIONS,
  ENTITY_TYPE_COLORS,
  ENTITY_TYPE_PIN_COLORS,
  DEFAULT_CAMERA_POSITION,
  TRACK_TRAIL_CONFIG,
} from './cesium-config';

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

class CircularBuffer<T> {
  private buffer: (T | undefined)[];
  private head = 0;
  private count = 0;

  constructor(private readonly capacity: number) {
    this.buffer = new Array(capacity);
  }

  push(item: T): void {
    this.buffer[this.head] = item;
    this.head = (this.head + 1) % this.capacity;
    if (this.count < this.capacity) this.count++;
  }

  get length(): number {
    return this.count;
  }

  toArray(): T[] {
    if (this.count === 0) return [];
    const result: T[] = new Array(this.count);
    const start = this.count < this.capacity ? 0 : this.head;
    for (let i = 0; i < this.count; i++) {
      result[i] = this.buffer[(start + i) % this.capacity] as T;
    }
    return result;
  }
}

@Component({
  selector: 'app-map',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule],
  templateUrl: './map.component.html',
  styleUrls: ['./map.component.scss'],
})
export class MapComponent implements AfterViewInit, OnDestroy {
  @ViewChild('cesiumContainer', { static: true })
  cesiumContainer!: ElementRef<HTMLDivElement>;

  viewer: any = null;
  selectedEntity = signal<Entity | null>(null);
  showLayerPanel = signal<boolean>(false);

  layers: LayerConfig[] = [
    { name: 'Persons', entityType: EntityType.PERSON, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.PERSON] },
    { name: 'Vehicles', entityType: EntityType.VEHICLE, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.VEHICLE] },
    { name: 'Vessels', entityType: EntityType.VESSEL, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.VESSEL] },
    { name: 'Aircraft', entityType: EntityType.AIRCRAFT, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.AIRCRAFT] },
    { name: 'Facilities', entityType: EntityType.FACILITY, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.FACILITY] },
    { name: 'Equipment', entityType: EntityType.EQUIPMENT, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.EQUIPMENT] },
    { name: 'Units', entityType: EntityType.UNIT, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.UNIT] },
    { name: 'Signals', entityType: EntityType.SIGNAL, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.SIGNAL] },
    { name: 'Cyber', entityType: EntityType.CYBER, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.CYBER] },
    { name: 'Satellites', entityType: EntityType.SATELLITE, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.SATELLITE] },
    { name: 'Unknown', entityType: EntityType.UNKNOWN, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.UNKNOWN] },
  ];

  private Cesium: any;
  private entityMap = new Map<string, any>(); // Cesium entity references
  private trackTrails = new Map<string, CircularBuffer<{ lat: number; lon: number; alt: number }>>();
  private subscriptions = new Subscription();
  private cameraMovedSubject = new Subject<void>();
  private cesiumColorCache = new Map<string, any>();
  private renderScheduled = false;
  private themePostProcessStage: any = null;

  constructor(
    private readonly ngZone: NgZone,
    private readonly entityService: EntityService,
    private readonly wsService: WebSocketService,
    private readonly themeService: ThemeService,
  ) {}

  async ngAfterViewInit(): Promise<void> {
    await this.initCesium();
    this.setupEventHandlers();
    this.subscribeToEntities();
    this.setupCameraMoveHandler();

    // Initial entity fetch — computeViewRectangle() returns undefined at high
    // altitude (view covers more than a hemisphere), so the camera moveEnd
    // handler won't trigger a fetch.  Load all entities eagerly.
    this.entityService.getEntities({ limit: 500 }).subscribe();

    this.subscribeToThemeChanges();
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
    if (this.viewer && !this.viewer.isDestroyed()) {
      this.viewer.destroy();
    }
  }

  private async initCesium(): Promise<void> {
    const Cesium = await import('cesium');
    this.Cesium = Cesium;

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
      });
    this.subscriptions.add(sub);

    this.viewer.camera.moveEnd.addEventListener(() => {
      this.cameraMovedSubject.next();
    });
  }

  private sendViewportBounds(): void {
    if (!this.viewer || !this.Cesium) return;

    const Cesium = this.Cesium;
    const canvas = this.viewer.scene.canvas;
    const camera = this.viewer.camera;

    try {
      const rect = camera.computeViewRectangle(
        this.viewer.scene.globe.ellipsoid,
        new Cesium.Rectangle(),
      );

      if (rect) {
        const bounds = {
          north: Cesium.Math.toDegrees(rect.north),
          south: Cesium.Math.toDegrees(rect.south),
          east: Cesium.Math.toDegrees(rect.east),
          west: Cesium.Math.toDegrees(rect.west),
        };

        this.wsService.sendViewportUpdate(bounds);

        // Also fetch entities in this viewport
        this.entityService
          .getEntities({ ...bounds, limit: 500 })
          .subscribe();
      } else {
        // computeViewRectangle() returns undefined when the camera sees the
        // full globe — fetch all entities without bounding-box filter.
        this.entityService.getEntities({ limit: 500 }).subscribe();
      }
    } catch {
      // Camera may not have a valid view rectangle (e.g., looking at sky)
    }
  }

  private subscribeToEntities(): void {
    // Subscribe to real-time entity updates
    const sub = this.entityService.entityUpdates$.subscribe((event) => {
      this.ngZone.runOutsideAngular(() => {
        this.handleEntityEvent(event);
      });
    });
    this.subscriptions.add(sub);

    // Subscribe to the full entity state — throttle to at most once/sec
    const entityStateSub = this.entityService.currentEntities$.pipe(
      throttleTime(1000),
    ).subscribe((entities) => {
      this.ngZone.runOutsideAngular(() => {
        entities.forEach((entity, id) => {
          if (!this.entityMap.has(id)) {
            this.addOrUpdateCesiumEntity(entity);
          }
        });
      });
    });
    this.subscriptions.add(entityStateSub);
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
    if (!this.viewer || !this.Cesium || !entity.position) return;

    const Cesium = this.Cesium;
    const layer = this.layers.find((l) => l.entityType === entity.entityType);
    if (layer && !layer.visible) return;

    const cesiumColor = this.getCesiumColor(entity.entityType);

    // Track trail
    this.updateTrackTrail(entity);
    const trailBuffer = this.trackTrails.get(entity.id);
    const trail = trailBuffer ? trailBuffer.toArray() : [];

    const position = Cesium.Cartesian3.fromDegrees(
      entity.position.longitude,
      entity.position.latitude,
      entity.position.altitude ?? 0,
    );

    const hasAltitude = entity.position.altitude != null && entity.position.altitude > 0;
    const heightRef = hasAltitude
      ? Cesium.HeightReference.NONE
      : Cesium.HeightReference.CLAMP_TO_GROUND;

    const existing = this.entityMap.get(entity.id);

    if (existing) {
      // Update existing entity
      existing.position = position;
      existing.label.text = entity.name;

      // Update polyline trail
      if (trail.length >= 2) {
        const trailPositions = trail.map((p) =>
          Cesium.Cartesian3.fromDegrees(p.lon, p.lat, p.alt),
        );
        existing.polyline.positions = trailPositions;
      }
    } else {
      // Create new entity
      const cesiumEntity = this.viewer.entities.add({
        position,
        point: {
          pixelSize: 10,
          color: cesiumColor,
          outlineColor: Cesium.Color.WHITE.withAlpha(0.5),
          outlineWidth: 1,
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
          heightReference: heightRef,
        },
        label: {
          text: entity.name,
          font: '12px JetBrains Mono, monospace',
          fillColor: Cesium.Color.WHITE,
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
                positions: trail.map((p) =>
                  Cesium.Cartesian3.fromDegrees(p.lon, p.lat, p.alt),
                ),
                width: TRACK_TRAIL_CONFIG.width,
                material: cesiumColor.withAlpha(TRACK_TRAIL_CONFIG.trailOpacity),
                clampToGround: !hasAltitude,
              }
            : undefined,
      });

      // Store reference to SENTINEL entity data
      cesiumEntity._sentinelEntity = entity;
      this.entityMap.set(entity.id, cesiumEntity);
    }

    // Update stored sentinel entity data
    if (this.entityMap.has(entity.id)) {
      this.entityMap.get(entity.id)._sentinelEntity = entity;
    }

    this.scheduleRender();
  }

  private removeCesiumEntity(entityId: string): void {
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
        cesiumEntity.show = layer.visible;
      }
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

  viewEntityDetails(entity: Entity): void {
    this.wsService.subscribeToEntity(entity.id);
    // Could navigate to entity detail panel
    console.log('View details for entity:', entity.id);
  }

  closeEntityPopup(): void {
    this.selectedEntity.set(null);
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
}
