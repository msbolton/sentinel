import { HttpClient, HttpParams } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { GLOBAL_BOUNDS, ViewportBounds, isGlobalBounds } from '../../features/map/viewport-bounds';
import { ReplayResponse, TimeWindow, TimelineEvent } from '../../shared/models/track.model';

/** Track history: activity buckets for the timeline and per-entity points for replay. */
@Injectable({ providedIn: 'root' })
export class TrackService {
  private readonly http = inject(HttpClient);
  private readonly url = '/api/v1/tracks';

  /** Event counts per time bucket; the bbox is left out for a global view. */
  timeline(window: TimeWindow, bounds: ViewportBounds | null, buckets = 120): Observable<TimelineEvent[]> {
    let params = this.windowParams(window).set('buckets', String(buckets));
    if (bounds && !isGlobalBounds(bounds)) params = params.set('bbox', this.bbox(bounds));
    return this.http.get<TimelineEvent[]>(`${this.url}/timeline`, { params });
  }

  /** The busiest tracks inside the box over the window; a global view sends the whole-globe box. */
  replay(window: TimeWindow, bounds: ViewportBounds, maxEntities = 500): Observable<ReplayResponse> {
    const box = isGlobalBounds(bounds) ? GLOBAL_BOUNDS : bounds;
    const params = this.windowParams(window).set('bbox', this.bbox(box)).set('maxEntities', String(maxEntities));
    return this.http.get<ReplayResponse>(`${this.url}/replay`, { params });
  }

  private windowParams(window: TimeWindow): HttpParams {
    return new HttpParams()
      .set('startTime', window.start.toISOString())
      .set('endTime', window.end.toISOString());
  }

  private bbox(b: ViewportBounds): string {
    return `${b.west},${b.south},${b.east},${b.north}`;
  }
}
