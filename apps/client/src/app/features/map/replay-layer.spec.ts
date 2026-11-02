import { ReplayLayer, sampledPositionFor } from './replay-layer';
import { CLUSTER_CONFIG } from './entity-clusters';
import { ENTITY_TYPE_COLORS } from './cesium-config';
import { Classification, Entity, EntitySource, EntityType } from '../../shared/models/entity.model';
import { ReplayPoint, ReplayResponse } from '../../shared/models/track.model';

const T0 = Date.parse('2026-11-02T10:00:00Z');

/** A Cesium stand-in: sampled positions answer by linear interpolation and never extrapolate. */
function makeFakeCesium() {
  const julian = (ms: number) => ({ ms });
  class SampledPositionProperty {
    samples: { time: { ms: number }; value: any }[] = [];
    interpolationAlgorithm: unknown = 'default';
    interpolationDegree = 0;
    forwardExtrapolationType: unknown = 'HOLD';
    backwardExtrapolationType: unknown = 'HOLD';
    addSample(time: { ms: number }, value: any) {
      this.samples.push({ time, value });
    }
    setInterpolationOptions(o: { interpolationAlgorithm?: unknown; interpolationDegree?: number }) {
      if (o.interpolationAlgorithm !== undefined) this.interpolationAlgorithm = o.interpolationAlgorithm;
      if (o.interpolationDegree !== undefined) this.interpolationDegree = o.interpolationDegree;
    }
    getValue(time: { ms: number }) {
      const s = this.samples;
      for (let i = 0; i < s.length; i++) {
        if (s[i].time.ms === time.ms) return s[i].value;
        const next = s[i + 1];
        if (next && s[i].time.ms < time.ms && time.ms < next.time.ms) {
          const f = (time.ms - s[i].time.ms) / (next.time.ms - s[i].time.ms);
          const mix = (a: number, b: number) => a + (b - a) * f;
          return { lon: mix(s[i].value.lon, next.value.lon), lat: mix(s[i].value.lat, next.value.lat), h: mix(s[i].value.h, next.value.h) };
        }
      }
      return undefined;
    }
  }
  class CustomDataSource {
    readonly list: any[] = [];
    readonly listeners: ((clustered: any[], cluster: any) => void)[] = [];
    readonly clustering = {
      enabled: false,
      pixelRange: 80,
      minimumClusterSize: 2,
      clusterEvent: { addEventListener: (fn: (clustered: any[], cluster: any) => void) => this.listeners.push(fn) },
    };
    readonly entities = {
      add: (cfg: any) => {
        this.list.push(cfg);
        return cfg;
      },
      removeAll: () => this.list.splice(0),
      getById: (id: string) => this.list.find((e) => e.id === id),
    };
    constructor(readonly name: string) {}
  }
  class Color {
    constructor(readonly red: number, readonly green: number, readonly blue: number, readonly alpha: number) {}
    withAlpha(a: number) {
      return new Color(this.red, this.green, this.blue, a);
    }
    static WHITE = new Color(1, 1, 1, 1);
    static BLACK = new Color(0, 0, 0, 1);
    static fromCssColorString = (css: string) => ({ css, withAlpha: (a: number) => ({ css, a }) });
  }
  return {
    SampledPositionProperty,
    CustomDataSource,
    Color,
    LinearApproximation: 'LinearApproximation',
    ExtrapolationType: { NONE: 'NONE', HOLD: 'HOLD' },
    JulianDate: { fromDate: (d: Date) => julian(d.getTime()), toDate: (j: { ms: number }) => new Date(j.ms) },
    TimeInterval: function (this: any, o: { start: any; stop: any }) {
      this.start = o.start;
      this.stop = o.stop;
    },
    TimeIntervalCollection: function (this: any, intervals: any[] = []) {
      this.intervals = intervals;
      this.start = intervals[0]?.start;
      this.stop = intervals[intervals.length - 1]?.stop;
    },
    Cartesian3: { fromDegrees: (lon: number, lat: number, h: number) => ({ lon, lat, h }) },
    Cartesian2: function (this: any, x: number, y: number) {
      this.x = x;
      this.y = y;
    },
    Cartographic: { fromCartesian: (c: any) => ({ longitude: (c.lon * Math.PI) / 180, latitude: (c.lat * Math.PI) / 180, height: c.h }) },
    Math: { toDegrees: (r: number) => (r * 180) / Math.PI },
    LabelStyle: { FILL_AND_OUTLINE: 'FILL_AND_OUTLINE' },
    VerticalOrigin: { CENTER: 'CENTER', BOTTOM: 'BOTTOM' },
  };
}

function rig() {
  const Cesium = makeFakeCesium();
  const sources: any[] = [];
  const viewer = {
    dataSources: {
      add: jest.fn((s: any) => sources.push(s)),
      remove: jest.fn((s: any) => sources.splice(sources.indexOf(s), 1)),
    },
    scene: { requestRender: jest.fn() },
    isDestroyed: () => false,
  };
  const billboardFor = jest.fn((e: Entity) => `pin:${e.entityType}`);
  const clusterImageFor = jest.fn((type: EntityType, count: number) => `disc:${type}:${count}`);
  const layer = new ReplayLayer(viewer, Cesium, { billboardFor, clusterImageFor });
  const entitiesOf = () => sources.flatMap((s) => s.list);
  return { Cesium, viewer, sources, layer, billboardFor, clusterImageFor, entitiesOf };
}

const pt = (dtMs: number, lon: number, lat: number, alt: number | null = 1000): ReplayPoint => [T0 + dtMs, lon, lat, alt, 90];

function response(entities: ReplayResponse['entities']): ReplayResponse {
  return { startTime: new Date(T0).toISOString(), endTime: new Date(T0 + 600_000).toISOString(), entities, truncated: false };
}

const plane = {
  entityId: 'a',
  entityType: EntityType.AIRCRAFT,
  name: 'BAW123',
  classification: Classification.UNCLASSIFIED,
  points: [pt(0, 0, 50), pt(60_000, 1, 51), pt(120_000, 2, 52)],
};
const ship = {
  entityId: 'b',
  entityType: EntityType.VESSEL,
  name: 'EVER GIVEN',
  classification: Classification.UNCLASSIFIED,
  points: [pt(30_000, -4, 49, null)],
};
const person = {
  entityId: 'c',
  entityType: EntityType.PERSON,
  name: 'Contact',
  classification: Classification.SECRET,
  points: [pt(0, 10, 10), pt(60_000, 11, 11)],
};

describe('sampledPositionFor', () => {
  it('samples every point linearly and never extrapolates past either end', () => {
    const Cesium = makeFakeCesium();
    const { position, availability } = sampledPositionFor(Cesium, plane.points);
    expect(position.samples).toEqual([
      { time: { ms: T0 }, value: { lon: 0, lat: 50, h: 1000 } },
      { time: { ms: T0 + 60_000 }, value: { lon: 1, lat: 51, h: 1000 } },
      { time: { ms: T0 + 120_000 }, value: { lon: 2, lat: 52, h: 1000 } },
    ]);
    expect(position.interpolationAlgorithm).toBe('LinearApproximation');
    expect(position.interpolationDegree).toBe(1);
    expect(position.forwardExtrapolationType).toBe('NONE');
    expect(position.backwardExtrapolationType).toBe('NONE');
    expect(availability.start).toEqual({ ms: T0 });
    expect(availability.stop).toEqual({ ms: T0 + 120_000 });
  });

  it('gives a one-point track a single sample and an interval whose start is its stop', () => {
    const Cesium = makeFakeCesium();
    const { position, availability } = sampledPositionFor(Cesium, ship.points);
    expect(position.samples).toEqual([{ time: { ms: T0 + 30_000 }, value: { lon: -4, lat: 49, h: 0 } }]);
    expect(availability.intervals).toHaveLength(1);
    expect(availability.start).toEqual(availability.stop);
  });
});

describe('ReplayLayer', () => {
  it('adds a data source per entity type with one sampled track per entity', () => {
    const { layer, sources, entitiesOf } = rig();
    layer.build(response([plane, ship]));

    expect(sources.map((s) => s.name)).toEqual(['sentinel-replay-aircraft', 'sentinel-replay-vessel']);
    const [a, b] = entitiesOf();
    expect(a.id).toBe('replay:a');
    expect(a.position.samples).toHaveLength(3);
    expect(b.id).toBe('replay:b');
    expect(b.position.samples).toHaveLength(1);
    expect(b.availability.start).toEqual(b.availability.stop);
    expect(layer.size).toBe(2);
  });

  it('draws each track as a type-coloured pin with its label and a ten-minute trailing path', () => {
    const { layer, entitiesOf, billboardFor } = rig();
    layer.build(response([plane]));

    const [a] = entitiesOf();
    const c = ENTITY_TYPE_COLORS[EntityType.AIRCRAFT];
    expect(billboardFor).toHaveBeenCalledWith(expect.objectContaining({ id: 'a', entityType: EntityType.AIRCRAFT, name: 'BAW123' }));
    expect(a.billboard).toEqual(
      expect.objectContaining({
        image: 'pin:AIRCRAFT',
        scale: 0.5,
        color: expect.objectContaining({ red: c.red, green: c.green, blue: c.blue, alpha: c.alpha }),
        disableDepthTestDistance: Number.POSITIVE_INFINITY,
        verticalOrigin: 'CENTER',
      }),
    );
    expect(a.label.text).toBe('BAW123');
    expect(a.path).toEqual(
      expect.objectContaining({
        trailTime: 600,
        leadTime: 0,
        width: 2,
        resolution: 30,
        material: expect.objectContaining({ red: c.red, green: c.green, blue: c.blue, alpha: 0.6 }),
      }),
    );
  });

  it('clusters the clustered types the way the live map does and leaves the others alone', () => {
    const { layer, sources, clusterImageFor } = rig();
    layer.build(response([plane, person]));

    const [aircraft, people] = sources;
    expect(aircraft.clustering).toEqual(
      expect.objectContaining({ enabled: true, pixelRange: CLUSTER_CONFIG.pixelRange, minimumClusterSize: CLUSTER_CONFIG.minimumClusterSize }),
    );
    expect(people.clustering.enabled).toBe(false);
    expect(people.listeners).toHaveLength(0);

    const cluster = { label: { show: true }, point: { show: true }, billboard: {} as any };
    const clustered = [{}, {}, {}];
    aircraft.listeners[0](clustered, cluster);
    expect(clusterImageFor).toHaveBeenCalledWith(EntityType.AIRCRAFT, 3);
    expect(cluster.label.show).toBe(false);
    expect(cluster.point.show).toBe(false);
    expect(cluster.billboard).toEqual({
      show: true,
      image: 'disc:AIRCRAFT:3',
      scale: 0.5,
      verticalOrigin: 'CENTER',
      disableDepthTestDistance: Number.POSITIVE_INFINITY,
      id: { _sentinelCluster: clustered },
    });
  });

  it('replaces the previous build, and clear removes every replay source', () => {
    const { layer, sources, entitiesOf, viewer } = rig();
    layer.build(response([plane, ship]));
    layer.build(response([person]));

    expect(sources.map((s) => s.name)).toEqual(['sentinel-replay-person']);
    expect(entitiesOf().map((e) => e.id)).toEqual(['replay:c']);
    expect(layer.size).toBe(1);

    layer.clear();
    expect(sources).toEqual([]);
    expect(viewer.dataSources.remove).toHaveBeenCalledTimes(3);
    expect(layer.size).toBe(0);
    expect(layer.entityAt('replay:c', new Date(T0))).toBeNull();
  });

  it('answers a selection with a synthetic entity where the track was at that moment', () => {
    const { layer } = rig();
    layer.build(response([plane, ship]));

    const e = layer.entityAt('replay:a', new Date(T0 + 30_000));
    expect(e).toEqual(
      expect.objectContaining({
        id: 'a',
        name: 'BAW123',
        entityType: EntityType.AIRCRAFT,
        classification: Classification.UNCLASSIFIED,
        source: EntitySource.UNSPECIFIED,
        metadata: {},
        affiliations: [],
      }),
    );
    expect(e!.position!.longitude).toBeCloseTo(0.5);
    expect(e!.position!.latitude).toBeCloseTo(50.5);
    expect(e!.position!.altitude).toBeCloseTo(1000);
  });

  it('has no entity for an unknown id or a moment the track does not cover', () => {
    const { layer } = rig();
    layer.build(response([plane]));

    expect(layer.entityAt('replay:zzz', new Date(T0))).toBeNull();
    expect(layer.entityAt('replay:a', new Date(T0 + 180_000))).toBeNull();
  });
});
