import type { DemoBounds } from '../../core/services/demo.service';
import { Emplacement } from '../../shared/models/emplacement.model';
import { boundsOf } from '../map/guest-map';

/** The positions of the active, placed sites that pass `keep`. */
function positions(list: Emplacement[], keep: (e: Emplacement) => boolean) {
  return list.filter((e) => e.active && e.position != null && keep(e)).map((e) => e.position!);
}

/** A box around the active sites that are not visitors; null when there are none. */
export function sensorBounds(list: Emplacement[], visitorsFeedId: string | null | undefined): DemoBounds | null {
  return boundsOf(positions(list, (e) => !visitorsFeedId || e.feedId !== visitorsFeedId));
}

/** A box around the active visitors' sites (the visitors connection); null when there are none. */
export function visitorBounds(list: Emplacement[], visitorsFeedId: string | null | undefined): DemoBounds | null {
  if (!visitorsFeedId) return null;
  return boundsOf(positions(list, (e) => e.feedId === visitorsFeedId));
}
