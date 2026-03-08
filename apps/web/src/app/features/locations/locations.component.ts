import { Component, OnInit, OnDestroy, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { HttpClient } from '@angular/common/http';
import { Subscription, Subject, debounceTime, switchMap, of, catchError } from 'rxjs';
import { Location, LocationCategory } from '../../shared/models/location.model';
import { LocationService } from '../../core/services/location.service';

interface GeoSearchResult {
  display_name: string;
  lat: string;
  lon: string;
  type: string;
  class: string;
}

@Component({
  selector: 'app-locations',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule],
  templateUrl: './locations.component.html',
  styleUrls: ['./locations.component.scss'],
})
export class LocationsComponent implements OnInit, OnDestroy {
  locations = signal<Location[]>([]);
  searchQuery = signal<string>('');
  selectedCategory = signal<LocationCategory | null>(null);
  showForm = signal<boolean>(false);
  editingLocation = signal<Location | null>(null);

  // Geocoding search
  geoSearchResults = signal<GeoSearchResult[]>([]);
  geoSearching = signal<boolean>(false);
  private geoSearch$ = new Subject<string>();

  categories = Object.values(LocationCategory);

  formData: Partial<Location> = this.getEmptyForm();

  filteredLocations = computed(() => {
    let list = this.locations();
    const query = this.searchQuery().toLowerCase();
    const category = this.selectedCategory();

    if (query) {
      list = list.filter((l) => l.name.toLowerCase().includes(query));
    }
    if (category) {
      list = list.filter((l) => l.category === category);
    }
    return list;
  });

  isCustomCategory = computed(() => this.formData.category === LocationCategory.CUSTOM);

  loading = computed(() => this.locationService.loading());

  private subscriptions = new Subscription();

  constructor(
    private readonly locationService: LocationService,
    private readonly http: HttpClient,
  ) {}

  ngOnInit(): void {
    const sub = this.locationService.locations$.subscribe((locations) => {
      this.locations.set(locations);
    });
    this.subscriptions.add(sub);
    this.locationService.loadLocations();

    // Geocoding search with debounce
    const geoSub = this.geoSearch$.pipe(
      debounceTime(400),
      switchMap((query) => {
        if (!query || query.length < 3) {
          return of([]);
        }
        this.geoSearching.set(true);
        return this.http.get<GeoSearchResult[]>(
          'https://nominatim.openstreetmap.org/search',
          {
            params: {
              q: query,
              format: 'json',
              limit: '6',
              addressdetails: '0',
            },
            headers: {
              'Accept': 'application/json',
            },
          },
        ).pipe(catchError(() => of([])));
      }),
    ).subscribe((results) => {
      this.geoSearchResults.set(results);
      this.geoSearching.set(false);
    });
    this.subscriptions.add(geoSub);
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
  }

  onSearchInput(value: string): void {
    this.searchQuery.set(value);
  }

  onGeoSearchInput(value: string): void {
    this.geoSearch$.next(value);
  }

  selectGeoResult(result: GeoSearchResult): void {
    this.formData.name = result.display_name.split(',')[0].trim();
    this.formData.description = result.display_name;
    this.formData.latitude = parseFloat(result.lat);
    this.formData.longitude = parseFloat(result.lon);
    this.formData.altitude = 1500;
    this.formData.heading = 0;
    this.formData.pitch = -45;
    this.formData.range = 3000;
    this.formData.category = this.inferCategory(result);
    this.geoSearchResults.set([]);
  }

  toggleCategory(category: LocationCategory): void {
    this.selectedCategory.set(
      this.selectedCategory() === category ? null : category,
    );
  }

  flyTo(location: Location): void {
    this.locationService.flyTo(location);
  }

  startAdd(): void {
    this.editingLocation.set(null);
    this.formData = this.getEmptyForm();
    this.geoSearchResults.set([]);
    this.showForm.set(true);
  }

  startEdit(location: Location): void {
    this.editingLocation.set(location);
    this.formData = { ...location };
    this.geoSearchResults.set([]);
    this.showForm.set(true);
  }

  cancelForm(): void {
    this.showForm.set(false);
    this.editingLocation.set(null);
    this.formData = this.getEmptyForm();
    this.geoSearchResults.set([]);
  }

  saveForm(): void {
    const editing = this.editingLocation();
    const { id, createdAt, updatedAt, createdBy, ...dto } = this.formData as any;
    if (editing) {
      this.locationService.updateLocation(editing.id, dto).subscribe({
        next: () => this.cancelForm(),
      });
    } else {
      this.locationService.createLocation(dto).subscribe({
        next: () => this.cancelForm(),
      });
    }
  }

  deleteLocation(location: Location): void {
    this.locationService.deleteLocation(location.id).subscribe();
  }

  getCategoryColor(category: LocationCategory): string {
    const colorMap: Record<LocationCategory, string> = {
      [LocationCategory.CITY]: 'var(--accent-blue)',
      [LocationCategory.MILITARY_BASE]: 'var(--accent-red)',
      [LocationCategory.PORT]: 'var(--accent-cyan)',
      [LocationCategory.AIRPORT]: 'var(--accent-yellow)',
      [LocationCategory.CUSTOM]: 'var(--accent-purple)',
    };
    return colorMap[category] ?? 'var(--text-muted)';
  }

  isFormValid(): boolean {
    return !!(
      this.formData.name &&
      this.formData.latitude != null &&
      this.formData.latitude >= -90 &&
      this.formData.latitude <= 90 &&
      this.formData.longitude != null &&
      this.formData.longitude >= -180 &&
      this.formData.longitude <= 180
    );
  }

  private inferCategory(result: GeoSearchResult): LocationCategory {
    const type = result.type?.toLowerCase() ?? '';
    const cls = result.class?.toLowerCase() ?? '';
    if (cls === 'place' && ['city', 'town', 'village', 'hamlet', 'suburb', 'borough', 'county', 'state', 'country'].includes(type)) {
      return LocationCategory.CITY;
    }
    if (type === 'aerodrome' || type === 'airport' || cls === 'aeroway') {
      return LocationCategory.AIRPORT;
    }
    if (type === 'port' || type === 'harbour' || type === 'marina' || type === 'dock') {
      return LocationCategory.PORT;
    }
    if (cls === 'military' || type === 'military') {
      return LocationCategory.MILITARY_BASE;
    }
    return LocationCategory.CUSTOM;
  }

  private getEmptyForm(): Partial<Location> {
    return {
      name: '',
      description: '',
      latitude: 0,
      longitude: 0,
      altitude: 1000,
      heading: 0,
      pitch: -45,
      range: 2000,
      has3dTiles: false,
      category: LocationCategory.CUSTOM,
    };
  }
}
