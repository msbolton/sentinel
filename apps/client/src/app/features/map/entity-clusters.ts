import { EntityType } from '../../shared/models/entity.model';

/**
 * Types that collapse into a counted disc when their pins crowd together on
 * screen. Cesium clusters per data source, so each gets its own; everything
 * else stays in the viewer's default collection.
 */
export const CLUSTERED_TYPES: EntityType[] = [EntityType.AIRCRAFT, EntityType.VESSEL, EntityType.VEHICLE];

const CLUSTERED = new Set<string>(CLUSTERED_TYPES);

/** The cluster group an entity type belongs to, or null for the default collection. */
export function clusterGroupFor(entityType: string): EntityType | null {
  return CLUSTERED.has(entityType) ? (entityType as EntityType) : null;
}

/** How a cluster of `count` pins is drawn: disc edge in px and the count text. */
export function clusterDiscSpec(count: number): { size: number; text: string } {
  const size = count < 10 ? 36 : count < 50 ? 40 : 44;
  const text = count > 99 ? '99+' : String(count);
  return { size, text };
}

/** Cesium EntityCluster settings shared by every clustered data source. */
export const CLUSTER_CONFIG = {
  pixelRange: 48,
  minimumClusterSize: 3,
};

/**
 * Draws the cluster disc: a filled circle in the type colour, a dark ring so
 * it separates from imagery, and the count centred in it. Returns the data
 * URL Cesium takes as a billboard image. Cached by the caller per type and text.
 */
export function drawClusterDisc(
  canvas: HTMLCanvasElement,
  spec: { size: number; text: string },
  fillColor: string,
  textColor: string,
): string {
  const px = spec.size * 2; // drawn at 2x, shown at scale 0.5 for crisp edges
  canvas.width = px;
  canvas.height = px;
  const ctx = canvas.getContext('2d');
  if (!ctx) return '';
  const r = px / 2;
  ctx.beginPath();
  ctx.arc(r, r, r - 3, 0, Math.PI * 2);
  ctx.fillStyle = fillColor;
  ctx.fill();
  ctx.lineWidth = 3;
  ctx.strokeStyle = 'rgba(10, 14, 23, 0.9)';
  ctx.stroke();
  ctx.fillStyle = textColor;
  ctx.font = `700 ${spec.text.length > 2 ? 22 : 26}px "JetBrains Mono", monospace`;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.fillText(spec.text, r, r + 1);
  return canvas.toDataURL();
}

/** The parts of a Cesium LabelCollection and Label this sync touches. */
interface GlyphLike { billboard?: { clusterShow: boolean } }
interface LabelLike { clusterShow: boolean; _glyphs?: GlyphLike[]; _backgroundBillboard?: { clusterShow: boolean } }
interface LabelCollectionLike { length: number; get(index: number): LabelLike }

/**
 * Cesium's EntityCluster hides a clustered entity's label by setting the
 * label's clusterShow, which the Label copies onto the glyph billboards it
 * has at that moment. Glyphs are created later, in the same frame, with the
 * default of visible, so the text keeps drawing over the cluster disc. Run
 * after each render: copies every label's flag onto its glyphs and
 * background. Returns whether anything changed, so the caller can request
 * one more frame.
 */
export function syncLabelGlyphClusterShow(labels: LabelCollectionLike | undefined): boolean {
  if (!labels) return false;
  let changed = false;
  for (let i = 0; i < labels.length; i++) {
    const label = labels.get(i);
    const want = label.clusterShow;
    for (const glyph of label._glyphs ?? []) {
      if (glyph.billboard && glyph.billboard.clusterShow !== want) {
        glyph.billboard.clusterShow = want;
        changed = true;
      }
    }
    const background = label._backgroundBillboard;
    if (background && background.clusterShow !== want) {
      background.clusterShow = want;
      changed = true;
    }
  }
  return changed;
}

/** The parts of a clustered entity billboard the trail sync touches. */
interface TrailedEntityLike { polyline?: { show: unknown }; _trailClusterShow?: boolean }
interface ClusterBillboardLike { id?: TrailedEntityLike; clusterShow: boolean }
interface BillboardCollectionLike { length: number; get(index: number): ClusterBillboardLike }

/**
 * Cesium's EntityCluster hides a clustered entity's billboard and label but
 * not its polyline, so trails would stick out of the cluster disc. Run after
 * each render: an entity's trail is shown exactly when its billboard is drawn
 * on its own. Returns whether anything changed, so the caller can request
 * one more frame.
 */
export function syncTrailClusterShow(billboards: BillboardCollectionLike | undefined): boolean {
  if (!billboards) return false;
  let changed = false;
  for (let i = 0; i < billboards.length; i++) {
    const billboard = billboards.get(i);
    const entity = billboard.id;
    if (!entity?.polyline) continue;
    const want = billboard.clusterShow;
    if (entity._trailClusterShow !== want) {
      entity.polyline.show = want;
      entity._trailClusterShow = want;
      changed = true;
    }
  }
  return changed;
}
