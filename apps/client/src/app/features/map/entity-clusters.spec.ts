import { EntityType } from '../../shared/models/entity.model';
import { CLUSTERED_TYPES, clusterDiscSpec, clusterGroupFor, syncLabelGlyphClusterShow, syncTrailClusterShow } from './entity-clusters';

describe('clusterGroupFor', () => {
  it('routes aircraft, vessels and vehicles to their own cluster group', () => {
    expect(clusterGroupFor(EntityType.AIRCRAFT)).toBe(EntityType.AIRCRAFT);
    expect(clusterGroupFor(EntityType.VESSEL)).toBe(EntityType.VESSEL);
    expect(clusterGroupFor(EntityType.VEHICLE)).toBe(EntityType.VEHICLE);
    expect(CLUSTERED_TYPES).toEqual([EntityType.AIRCRAFT, EntityType.VESSEL, EntityType.VEHICLE]);
  });

  it('leaves every other type in the default collection', () => {
    for (const type of [EntityType.SATELLITE, EntityType.FACILITY, EntityType.PERSON, EntityType.SIGNAL, EntityType.DRONE, EntityType.UNKNOWN]) {
      expect(clusterGroupFor(type)).toBeNull();
    }
  });
});

describe('clusterDiscSpec', () => {
  it('grows the disc with the count and caps the text at 99+', () => {
    expect(clusterDiscSpec(3)).toEqual({ size: 36, text: '3' });
    expect(clusterDiscSpec(12)).toEqual({ size: 40, text: '12' });
    expect(clusterDiscSpec(57)).toEqual({ size: 44, text: '57' });
    expect(clusterDiscSpec(140)).toEqual({ size: 44, text: '99+' });
  });
});

describe('syncLabelGlyphClusterShow', () => {
  function label(clusterShow: boolean, glyphShows: boolean[], background?: boolean) {
    return {
      clusterShow,
      _glyphs: glyphShows.map((v) => ({ billboard: { clusterShow: v } })),
      _backgroundBillboard: background === undefined ? undefined : { clusterShow: background },
    };
  }
  function collection(labels: ReturnType<typeof label>[]) {
    return { length: labels.length, get: (i: number) => labels[i] };
  }

  it('pushes a hidden label onto glyphs that Cesium created after the flag was set', () => {
    const hidden = label(false, [true, true, false], true);
    const changed = syncLabelGlyphClusterShow(collection([hidden]));
    expect(changed).toBe(true);
    expect(hidden._glyphs.map((g) => g.billboard.clusterShow)).toEqual([false, false, false]);
    expect(hidden._backgroundBillboard?.clusterShow).toBe(false);
  });

  it('restores glyphs of a label that is shown again', () => {
    const shown = label(true, [false], false);
    expect(syncLabelGlyphClusterShow(collection([shown]))).toBe(true);
    expect(shown._glyphs[0].billboard.clusterShow).toBe(true);
    expect(shown._backgroundBillboard?.clusterShow).toBe(true);
  });

  it('reports no change when every glyph already agrees', () => {
    expect(syncLabelGlyphClusterShow(collection([label(false, [false, false], false), label(true, [true])]))).toBe(false);
    expect(syncLabelGlyphClusterShow(undefined)).toBe(false);
  });
});

describe('syncTrailClusterShow', () => {
  const collection = (items: { id?: any; clusterShow: boolean }[]) => ({ length: items.length, get: (i: number) => items[i] });

  it('hides the trail of an entity drawn inside a cluster and brings it back when it leaves', () => {
    const clustered = { polyline: { show: true as unknown } };
    const alone = { polyline: { show: true as unknown } };
    const noTrail = {};
    const items = [{ id: clustered, clusterShow: false }, { id: alone, clusterShow: true }, { id: noTrail, clusterShow: false }, { clusterShow: false }];

    expect(syncTrailClusterShow(collection(items))).toBe(true);
    expect(clustered.polyline.show).toBe(false);
    expect(alone.polyline.show).toBe(true);
    expect(syncTrailClusterShow(collection(items))).toBe(false); // nothing to do

    items[0].clusterShow = true;
    expect(syncTrailClusterShow(collection(items))).toBe(true);
    expect(clustered.polyline.show).toBe(true);
  });

  it('tolerates a source with no billboards yet', () => {
    expect(syncTrailClusterShow(undefined)).toBe(false);
  });
});
