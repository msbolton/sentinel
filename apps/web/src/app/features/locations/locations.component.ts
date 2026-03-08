import { Component, OnInit, OnDestroy, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Subscription } from 'rxjs';
import { Location, LocationCategory } from '../../shared/models/location.model';
import { LocationService } from '../../core/services/location.service';

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

  private subscriptions = new Subscription();

  constructor(private readonly locationService: LocationService) {}

  ngOnInit(): void {
    const sub = this.locationService.locations$.subscribe((locations) => {
      this.locations.set(locations);
    });
    this.subscriptions.add(sub);
    this.locationService.loadLocations();
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
  }

  onSearchInput(value: string): void {
    this.searchQuery.set(value);
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
    this.showForm.set(true);
  }

  startEdit(location: Location): void {
    this.editingLocation.set(location);
    this.formData = { ...location };
    this.showForm.set(true);
  }

  cancelForm(): void {
    this.showForm.set(false);
    this.editingLocation.set(null);
    this.formData = this.getEmptyForm();
  }

  saveForm(): void {
    const editing = this.editingLocation();
    if (editing) {
      this.locationService.updateLocation(editing.id, this.formData).subscribe({
        next: () => this.cancelForm(),
      });
    } else {
      this.locationService.createLocation(this.formData).subscribe({
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
