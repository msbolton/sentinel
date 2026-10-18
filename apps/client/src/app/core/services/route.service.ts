import { Injectable, inject, signal } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Subscription } from 'rxjs';
import { Entity } from '../../shared/models/entity.model';
import { AircraftRoute, routeQueryOf } from '../../shared/models/route.model';

export interface RouteState {
  key: string | null; // hex|callsign of the aircraft asked about
  loading: boolean;
  route: AircraftRoute | null;
  error: string | null;
}

const EMPTY: RouteState = { key: null, loading: false, route: null, error: null };

/**
 * The selected aircraft's departure and arrival, through the gateway
 * (scheduled routes from adsbdb by callsign; for IntelSky aircraft without
 * one, where IntelSky first saw it today). The map follows the selection
 * with `select`; the map and the detail panel read `state`.
 */
@Injectable({ providedIn: 'root' })
export class RouteService {
  private readonly http = inject(HttpClient);
  private readonly stateSignal = signal<RouteState>(EMPTY);
  private request: Subscription | null = null;

  readonly state = this.stateSignal.asReadonly();

  select(entity: Entity | null): void {
    const q = routeQueryOf(entity);
    const key = q ? `${q.hex}|${q.callsign ?? ''}` : null;
    if (key === this.stateSignal().key) return;
    this.request?.unsubscribe();
    this.request = null;
    if (!q) {
      this.stateSignal.set(EMPTY);
      return;
    }
    this.stateSignal.set({ key, loading: true, route: null, error: null });
    const params: Record<string, string> = {};
    if (q.callsign) params['callsign'] = q.callsign;
    if (q.intelsky) params['source'] = 'INTELSKY';
    this.request = this.http.get<AircraftRoute>(`/api/v1/aircraft/${q.hex}/route`, { params }).subscribe({
      next: (route) => this.stateSignal.set({ key, loading: false, route, error: null }),
      error: () => this.stateSignal.set({ key, loading: false, route: null, error: 'Route unavailable.' }),
    });
  }
}
