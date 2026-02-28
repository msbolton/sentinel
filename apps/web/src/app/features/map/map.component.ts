import {
  Component,
  AfterViewInit,
  OnDestroy,
  ViewChild,
  ElementRef,
  signal,
  NgZone,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Subscription } from 'rxjs';
import { debounceTime, Subject } from 'rxjs';
import {
  Entity,
  EntityType,
  EntityEvent,
} from '../../shared/models/entity.model';
import { EntityService } from '../../core/services/entity.service';
import { WebSocketService } from '../../core/services/websocket.service';
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

interface LayerConfig {
  name: string;
  entityType: EntityType;
  visible: boolean;
  color: string;
}

@Component({
  selector: 'app-map',
  standalone: true,
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
    { name: 'Unknown', entityType: EntityType.UNKNOWN, visible: true, color: ENTITY_TYPE_PIN_COLORS[EntityType.UNKNOWN] },
  ];

  private Cesium: any;
  private entityMap = new Map<string, any>(); // Cesium entity references
  private trackTrails = new Map<string, Array<{ lat: number; lon: number }>>();
  private subscriptions = new Subscription();
  private cameraMovedSubject = new Subject<void>();

  constructor(
    private readonly ngZone: NgZone,
    private readonly entityService: EntityService,
    private readonly wsService: WebSocketService,
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

    // Subscribe to the full entity state
    const entityStateSub = this.entityService.currentEntities$.subscribe((entities) => {
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

    const color = ENTITY_TYPE_COLORS[entity.entityType] ?? ENTITY_TYPE_COLORS[EntityType.UNKNOWN];
    const cesiumColor = new Cesium.Color(color.red, color.green, color.blue, color.alpha);

    // Track trail
    this.updateTrackTrail(entity);
    const trail = this.trackTrails.get(entity.id) ?? [];

    const position = Cesium.Cartesian3.fromDegrees(
      entity.position.longitude,
      entity.position.latitude,
      entity.position.altitude ?? 0,
    );

    const existing = this.entityMap.get(entity.id);

    if (existing) {
      // Update existing entity
      existing.position = position;
      existing.label.text = entity.name;

      // Update polyline trail
      if (trail.length >= 2) {
        const trailPositions = trail.map((p) =>
          Cesium.Cartesian3.fromDegrees(p.lon, p.lat),
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
          heightReference: Cesium.HeightReference.CLAMP_TO_GROUND,
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
        },
        polyline:
          trail.length >= 2
            ? {
                positions: trail.map((p) =>
                  Cesium.Cartesian3.fromDegrees(p.lon, p.lat),
                ),
                width: TRACK_TRAIL_CONFIG.width,
                material: cesiumColor.withAlpha(TRACK_TRAIL_CONFIG.trailOpacity),
                clampToGround: true,
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

    this.viewer.scene.requestRender();
  }

  private removeCesiumEntity(entityId: string): void {
    const cesiumEntity = this.entityMap.get(entityId);
    if (cesiumEntity && this.viewer) {
      this.viewer.entities.remove(cesiumEntity);
      this.entityMap.delete(entityId);
      this.trackTrails.delete(entityId);
      this.viewer.scene.requestRender();
    }
  }

  private updateTrackTrail(entity: Entity): void {
    if (!entity.position) return;

    let trail = this.trackTrails.get(entity.id);
    if (!trail) {
      trail = [];
      this.trackTrails.set(entity.id, trail);
    }

    trail.push({
      lat: entity.position.latitude,
      lon: entity.position.longitude,
    });

    // Limit trail length
    if (trail.length > TRACK_TRAIL_CONFIG.maxPoints) {
      trail.splice(0, trail.length - TRACK_TRAIL_CONFIG.maxPoints);
    }
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
    if (this.viewer) {
      this.viewer.scene.requestRender();
    }
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
}
