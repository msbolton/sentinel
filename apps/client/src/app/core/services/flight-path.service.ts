import { Injectable, inject, signal } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Subscription } from 'rxjs';
import { Entity } from '../../shared/models/entity.model';
import { FlightPath, FlightPathDays, defaultFlightPathDays, icaoHexOf } from '../../shared/models/flight-path.model';

/** The selected aircraft's past path and how far back it goes. */
export interface FlightPathState {
  hex: string | null;
  days: FlightPathDays;
  loading: boolean;
  path: FlightPath | null;
  error: string | null;
}

const EMPTY: FlightPathState = { hex: null, days: 0, loading: false, path: null, error: null };

/**
 * Where the selected military aircraft has flown, from IntelSky through the
 * gateway. The map follows the selection with `select`, the detail panel
 * changes the range with `setDays`, and both read `state`.
 */
@Injectable({ providedIn: 'root' })
export class FlightPathService {
  private readonly http = inject(HttpClient);
  private readonly stateSignal = signal<FlightPathState>(EMPTY);
  private request: Subscription | null = null;

  readonly state = this.stateSignal.asReadonly();

  /** A new selection: pick its default range and load it. */
  select(entity: Entity | null): void {
    const hex = icaoHexOf(entity);
    if (hex === this.stateSignal().hex) return;
    this.load(hex, defaultFlightPathDays(entity));
  }

  setDays(days: FlightPathDays): void {
    const { hex } = this.stateSignal();
    if (hex) this.load(hex, days);
  }

  private load(hex: string | null, days: FlightPathDays): void {
    this.request?.unsubscribe();
    this.request = null;
    if (!hex || days === 0) {
      this.stateSignal.set({ ...EMPTY, hex, days: hex ? days : 0 });
      return;
    }
    this.stateSignal.set({ hex, days, loading: true, path: null, error: null });
    this.request = this.http.get<FlightPath>(`/api/v1/aircraft/${hex}/path`, { params: { days } }).subscribe({
      next: (path) => this.stateSignal.set({ hex, days, loading: false, path, error: null }),
      error: (err: HttpErrorResponse) =>
        this.stateSignal.set({
          hex, days, loading: false, path: null,
          error: err.status === 503 ? 'IntelSky is busy; try again shortly.' : 'Flight path unavailable.',
        }),
    });
  }
}
