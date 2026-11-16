import { Entity, EntitySource, EntityType } from '../../shared/models/entity.model';
import { ReplayEntity, ReplayPoint, ReplayResponse } from '../../shared/models/track.model';
import { monoFont } from '../../shared/mono-font';
import { ENTITY_TYPE_COLORS } from './cesium-config';
import { CLUSTER_CONFIG, CLUSTERED_TYPES } from './entity-clusters';
import { entityLabel } from './entity-label';

/** The images the map draws with, so a replayed track looks like a live one. */
export interface ReplayImages {
  billboardFor: (entity: Entity) => string;
  clusterImageFor: (type: EntityType, count: number) => string;
}

/** How long a replayed track's trailing path is, in seconds of replay time. */
const TRAIL_SECONDS = 600;

/**
 * A track's position over time: one sample per point, interpolated linearly
 * and undefined outside the first and last point, so the entity disappears
 * rather than freezing at an end. The availability runs from the first point
 * to the last; a one-point track is available for that one instant.
 */
export function sampledPositionFor(Cesium: any, points: ReplayPoint[]): { position: any; availability: any } {
  const position = new Cesium.SampledPositionProperty();
  // interpolationAlgorithm/Degree are read-only getters on the real property.
  position.setInterpolationOptions({ interpolationAlgorithm: Cesium.LinearApproximation, interpolationDegree: 1 });
  position.forwardExtrapolationType = Cesium.ExtrapolationType.NONE;
  position.backwardExtrapolationType = Cesium.ExtrapolationType.NONE;
  for (const [ms, lon, lat, alt] of points) {
    position.addSample(Cesium.JulianDate.fromDate(new Date(ms)), Cesium.Cartesian3.fromDegrees(lon, lat, alt ?? 0));
  }
  const intervals =
    points.length > 0
      ? [
          new Cesium.TimeInterval({
            start: Cesium.JulianDate.fromDate(new Date(points[0][0])),
            stop: Cesium.JulianDate.fromDate(new Date(points[points.length - 1][0])),
          }),
        ]
      : [];
  return { position, availability: new Cesium.TimeIntervalCollection(intervals) };
}

/** A replayed track as an Entity, with no position until one is known. */
function replayEntity(r: ReplayEntity, updatedAt: string): Entity {
  return {
    id: r.entityId,
    name: r.name,
    entityType: r.entityType,
    classification: r.classification,
    source: EntitySource.UNSPECIFIED,
    metadata: {},
    affiliations: [],
    createdAt: updatedAt,
    updatedAt,
  };
}

/**
 * Draws a timeline replay: every track in a replay response as a pin moving
 * along its sampled positions under the viewer's clock, with a ten-minute
 * trailing path. Tracks live in their own data sources, one per entity type
 * and named `sentinel-replay-<type>`, clustered like the live ones, so the
 * replay never mixes with live entities. Cesium entity ids are
 * `replay:<entityId>`. Heading is not drawn yet; pins are unrotated.
 */
export class ReplayLayer {
  private readonly sources = new Map<EntityType, any>();
  private readonly tracks = new Map<string, { entity: ReplayEntity; position: any }>();

  constructor(
    private readonly viewer: any,
    private readonly Cesium: any,
    private readonly images: ReplayImages,
  ) {}

  /** How many tracks are drawn. */
  get size(): number {
    return this.tracks.size;
  }

  /** Draws a response in place of whatever was drawn before. Tracks with no points are skipped. */
  build(response: ReplayResponse): void {
    this.clear();
    const C = this.Cesium;
    const updatedAt = response.endTime;
    for (const r of response.entities) {
      if (r.points.length === 0) continue;
      const { position, availability } = sampledPositionFor(C, r.points);
      const rgba = ENTITY_TYPE_COLORS[r.entityType] ?? ENTITY_TYPE_COLORS[EntityType.UNKNOWN];
      const color = new C.Color(rgba.red, rgba.green, rgba.blue, rgba.alpha);
      this.sourceFor(r.entityType).entities.add({
        id: `replay:${r.entityId}`,
        position,
        availability,
        billboard: {
          image: this.images.billboardFor(replayEntity(r, updatedAt)),
          scale: 0.5,
          color,
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
          verticalOrigin: C.VerticalOrigin.CENTER,
        },
        label: {
          text: entityLabel(r.name, undefined),
          font: `12px ${monoFont()}`,
          fillColor: C.Color.WHITE,
          outlineColor: C.Color.BLACK,
          outlineWidth: 2,
          style: C.LabelStyle.FILL_AND_OUTLINE,
          verticalOrigin: C.VerticalOrigin.BOTTOM,
          pixelOffset: new C.Cartesian2(0, -16),
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
          showBackground: true,
          backgroundColor: C.Color.fromCssColorString('#0a0e17').withAlpha(0.7),
          backgroundPadding: new C.Cartesian2(6, 4),
        },
        path: {
          trailTime: TRAIL_SECONDS,
          leadTime: 0,
          width: 2,
          resolution: 30,
          material: color.withAlpha(0.6),
        },
      });
      this.tracks.set(`replay:${r.entityId}`, { entity: r, position });
    }
    this.viewer.scene.requestRender();
  }

  /** Removes every replay data source. */
  clear(): void {
    if (!this.viewer.isDestroyed()) {
      for (const source of this.sources.values()) this.viewer.dataSources.remove(source, true);
      this.viewer.scene.requestRender();
    }
    this.sources.clear();
    this.tracks.clear();
  }

  /**
   * The track drawn as `cesiumEntityId`, as an Entity placed where it was at
   * `at`; null for an id this layer did not draw or a moment the track does
   * not cover.
   */
  entityAt(cesiumEntityId: string, at: Date): Entity | null {
    const track = this.tracks.get(cesiumEntityId);
    if (!track) return null;
    const C = this.Cesium;
    const cartesian = track.position.getValue(C.JulianDate.fromDate(at));
    if (!cartesian) return null;
    const carto = C.Cartographic.fromCartesian(cartesian);
    return {
      ...replayEntity(track.entity, at.toISOString()),
      position: {
        latitude: C.Math.toDegrees(carto.latitude),
        longitude: C.Math.toDegrees(carto.longitude),
        altitude: carto.height,
      },
    };
  }

  private sourceFor(type: EntityType): any {
    const existing = this.sources.get(type);
    if (existing) return existing;
    const C = this.Cesium;
    const source = new C.CustomDataSource(`sentinel-replay-${type.toLowerCase()}`);
    if (CLUSTERED_TYPES.includes(type)) {
      source.clustering.enabled = true;
      source.clustering.pixelRange = CLUSTER_CONFIG.pixelRange;
      source.clustering.minimumClusterSize = CLUSTER_CONFIG.minimumClusterSize;
      source.clustering.clusterEvent.addEventListener((clustered: any[], cluster: any) => {
        cluster.label.show = false;
        cluster.point.show = false;
        cluster.billboard.show = true;
        cluster.billboard.image = this.images.clusterImageFor(type, clustered.length);
        cluster.billboard.scale = 0.5;
        cluster.billboard.verticalOrigin = C.VerticalOrigin.CENTER;
        cluster.billboard.disableDepthTestDistance = Number.POSITIVE_INFINITY;
        cluster.billboard.id = { _sentinelCluster: clustered };
      });
    }
    this.viewer.dataSources.add(source);
    this.sources.set(type, source);
    return source;
  }
}
