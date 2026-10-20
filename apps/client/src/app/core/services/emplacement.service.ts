import { Injectable, computed, inject, signal } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Observable, map, tap } from 'rxjs';
import { AuthService } from './auth.service';
import { Emplacement, EmplacementDraft, EmplacementInput, EmplacementPoint } from '../../shared/models/emplacement.model';

const URL = '/api/v1/emplacements';
const COVERAGE_KEY = 'sentinel-emplacement-coverage';

/** The roles that may create and edit emplacements (inside their connection), matching the gateway's policy. */
export const EMPLACEMENT_WRITER_ROLES = ['sentinel-operator', 'sentinel-admin'];
/** Deleting one is for admins. */
export const EMPLACEMENT_DELETE_ROLES = ['sentinel-admin'];

const byName = (list: Emplacement[]) => [...list].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));

function readShowCoverage(): boolean {
  try {
    return localStorage.getItem(COVERAGE_KEY) !== 'false';
  } catch {
    return true;
  }
}

/**
 * The client's one view of emplacements: the list, the selection, the form's
 * unsaved draft (which the map previews) and whether the map's next click
 * places it.
 */
@Injectable({ providedIn: 'root' })
export class EmplacementService {
  private readonly http = inject(HttpClient);
  private readonly auth = inject(AuthService);

  readonly canWrite = computed(() => this.auth.hasAnyRole(...EMPLACEMENT_WRITER_ROLES));
  readonly canDelete = computed(() => this.auth.hasAnyRole(...EMPLACEMENT_DELETE_ROLES));

  readonly emplacements = signal<Emplacement[]>([]);
  readonly selectedId = signal<string | null>(null);
  readonly selected = computed(() => this.emplacements().find((e) => e.id === this.selectedId()) ?? null);
  readonly draft = signal<EmplacementDraft | null>(null);
  readonly picking = signal(false);
  /** Whether coverage is drawn on the map: this user's choice. */
  readonly showCoverage = signal<boolean>(readShowCoverage());
  /** A place the map should fly to ("Show on map" in an editor); the map clears it once there. */
  readonly focus = signal<EmplacementPoint | null>(null);

  load(): Observable<Emplacement[]> {
    return this.http.get<Emplacement[]>(URL).pipe(
      map((list) => byName(list ?? [])),
      tap((list) => this.emplacements.set(list)),
    );
  }

  create(input: EmplacementInput): Observable<Emplacement> {
    return this.http.post<Emplacement>(URL, input).pipe(tap((e) => this.emplacements.update((l) => byName([...l, e]))));
  }

  /**
   * Saves a change to the version the user edited, which the caller names: the
   * list may have been refreshed to a newer one since. A newer one on the
   * server answers 412.
   */
  update(id: string, version: number, input: EmplacementInput): Observable<Emplacement> {
    return this.http
      .patch<Emplacement>(`${URL}/${id}`, input, { headers: { 'If-Match': `"${version}"` } })
      .pipe(tap((u) => this.emplacements.update((l) => byName(l.map((x) => (x.id === u.id ? u : x))))));
  }

  remove(id: string): Observable<void> {
    return this.http.delete<void>(`${URL}/${id}`).pipe(
      tap(() => {
        this.emplacements.update((l) => l.filter((e) => e.id !== id));
        if (this.selectedId() === id) this.selectedId.set(null);
      }),
    );
  }

  select(id: string | null): void {
    this.selectedId.set(id);
  }

  /** The map's click while picking: the draft's new position. */
  pickAt(lat: number, lon: number): void {
    this.picking.set(false);
    this.draft.update((d) => (d ? { ...d, position: { lat, lon } } : d));
  }

  setShowCoverage(show: boolean): void {
    this.showCoverage.set(show);
    try {
      localStorage.setItem(COVERAGE_KEY, String(show));
    } catch {
      // private window or blocked storage: the choice lasts this session
    }
  }
}

/** What to tell the user about a failed save or delete. */
export function errorMessage(err: unknown): string {
  if (err instanceof HttpErrorResponse) {
    if (err.status === 412) return 'Changed by someone else since you opened it. Reload to see the latest.';
    if (err.status === 422 && Array.isArray(err.error?.details) && err.error.details.length > 0) {
      return err.error.details.map((p: { path: string; message: string }) => `${p.path.replace(/^\//, '')} ${p.message}`).join('; ');
    }
    if (typeof err.error?.error === 'string') return err.error.error;
  }
  return 'Could not save the emplacement.';
}
