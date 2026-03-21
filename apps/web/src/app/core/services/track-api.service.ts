import { Injectable } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Observable, Subscriber } from 'rxjs';
import { AuthService } from './auth.service';

export interface TrackPoint {
  id: string;
  entityId: string;
  latitude: number;
  longitude: number;
  altitude: number | null;
  heading: number | null;
  speedKnots: number | null;
  course: number | null;
  source: string | null;
  timestamp: string;
}

export interface TrackHistoryParams {
  startTime?: string;
  endTime?: string;
  maxPoints?: number;
  simplify?: number;
}

export interface ReplayParams {
  startTime: string;
  endTime: string;
  speedMultiplier?: number;
}

export interface TrackSegment {
  startTime: string;
  endTime: string;
  points: TrackPoint[];
}

@Injectable({ providedIn: 'root' })
export class TrackApiService {
  private readonly apiUrl = '/api/tracks';

  constructor(
    private readonly http: HttpClient,
    private readonly authService: AuthService,
  ) {}

  getHistory(entityId: string, params?: TrackHistoryParams): Observable<TrackPoint[]> {
    let httpParams = new HttpParams();
    if (params) {
      if (params.startTime) httpParams = httpParams.set('startTime', params.startTime);
      if (params.endTime) httpParams = httpParams.set('endTime', params.endTime);
      if (params.maxPoints) httpParams = httpParams.set('maxPoints', params.maxPoints.toString());
      if (params.simplify) httpParams = httpParams.set('simplify', params.simplify.toString());
    }
    return this.http.get<TrackPoint[]>(`${this.apiUrl}/${entityId}`, { params: httpParams });
  }

  getLatestPosition(entityId: string): Observable<TrackPoint | null> {
    return this.http.get<TrackPoint | null>(`${this.apiUrl}/${entityId}/latest`);
  }

  getSegments(entityId: string, startTime?: string, endTime?: string): Observable<TrackSegment[]> {
    let httpParams = new HttpParams();
    if (startTime) httpParams = httpParams.set('startTime', startTime);
    if (endTime) httpParams = httpParams.set('endTime', endTime);
    return this.http.get<TrackSegment[]>(`${this.apiUrl}/${entityId}/segments`, { params: httpParams });
  }

  replayStream(entityId: string, params: ReplayParams): Observable<TrackPoint> {
    return new Observable<TrackPoint>((subscriber: Subscriber<TrackPoint>) => {
      const token = this.authService.getToken();
      const queryParts = [
        `startTime=${encodeURIComponent(params.startTime)}`,
        `endTime=${encodeURIComponent(params.endTime)}`,
      ];
      if (params.speedMultiplier) {
        queryParts.push(`speedMultiplier=${params.speedMultiplier}`);
      }
      if (token) {
        queryParts.push(`token=${encodeURIComponent(token)}`);
      }

      const url = `${this.apiUrl}/${entityId}/replay-stream?${queryParts.join('&')}`;
      const eventSource = new EventSource(url);

      eventSource.addEventListener('point', (event: MessageEvent) => {
        try {
          subscriber.next(JSON.parse(event.data));
        } catch (e) {
          subscriber.error(e);
        }
      });

      eventSource.addEventListener('complete', () => {
        eventSource.close();
        subscriber.complete();
      });

      eventSource.onerror = () => {
        eventSource.close();
        subscriber.error(new Error('SSE connection error'));
      };

      return () => {
        eventSource.close();
      };
    });
  }
}
