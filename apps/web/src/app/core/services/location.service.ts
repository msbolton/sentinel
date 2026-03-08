import { Injectable, signal } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { BehaviorSubject, Subject, Observable, tap } from 'rxjs';
import { Location, LocationCategory } from '../../shared/models/location.model';

@Injectable({ providedIn: 'root' })
export class LocationService {
  private readonly apiUrl = '/api/v1/locations';

  private locationsSubject = new BehaviorSubject<Location[]>([]);
  locations$ = this.locationsSubject.asObservable();
  loading = signal<boolean>(false);

  flyTo$ = new Subject<Location>();

  constructor(private readonly http: HttpClient) {}

  loadLocations(category?: LocationCategory): void {
    this.loading.set(true);
    let params = new HttpParams();
    if (category) {
      params = params.set('category', category);
    }
    this.http.get<Location[]>(this.apiUrl, { params }).subscribe({
      next: (locations) => {
        this.locationsSubject.next(locations);
        this.loading.set(false);
      },
      error: (err) => {
        console.error('[LocationService] Failed to load locations:', err);
        this.loading.set(false);
      },
    });
  }

  createLocation(dto: Partial<Location>): Observable<Location> {
    return this.http.post<Location>(this.apiUrl, dto).pipe(
      tap(() => this.loadLocations()),
    );
  }

  updateLocation(id: string, dto: Partial<Location>): Observable<Location> {
    return this.http.patch<Location>(`${this.apiUrl}/${id}`, dto).pipe(
      tap(() => this.loadLocations()),
    );
  }

  deleteLocation(id: string): Observable<void> {
    return this.http.delete<void>(`${this.apiUrl}/${id}`).pipe(
      tap(() => this.loadLocations()),
    );
  }

  flyTo(location: Location): void {
    this.flyTo$.next(location);
  }
}
