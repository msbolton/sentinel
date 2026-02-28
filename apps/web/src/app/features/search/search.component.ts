import { Component, OnInit, OnDestroy, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Subject, Subscription, debounceTime, distinctUntilChanged, switchMap, of } from 'rxjs';
import {
  Entity,
  EntityType,
  EntitySource,
  Classification,
  PaginatedResponse,
} from '../../shared/models/entity.model';
import { EntityService } from '../../core/services/entity.service';
import { MapComponent } from '../map/map.component';

@Component({
  selector: 'app-search',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule],
  templateUrl: './search.component.html',
  styleUrls: ['./search.component.scss'],
})
export class SearchComponent implements OnInit, OnDestroy {
  searchQuery = signal<string>('');
  results = signal<Entity[]>([]);
  totalResults = signal<number>(0);
  loading = signal<boolean>(false);
  selectedEntityTypes = signal<Set<EntityType>>(new Set());
  selectedSources = signal<Set<EntitySource>>(new Set());
  selectedClassification = signal<Classification | null>(null);
  geoBounded = signal<boolean>(false);

  entityTypes = Object.values(EntityType);
  entitySources = Object.values(EntitySource);
  classifications = Object.values(Classification);

  private searchSubject = new Subject<string>();
  private subscriptions = new Subscription();

  constructor(private readonly entityService: EntityService) {}

  ngOnInit(): void {
    const sub = this.searchSubject
      .pipe(
        debounceTime(300),
        distinctUntilChanged(),
        switchMap((query) => {
          if (!query || query.trim().length < 2) {
            return of({ data: [], total: 0, limit: 50, offset: 0 } as PaginatedResponse<Entity>);
          }
          this.loading.set(true);
          return this.entityService.searchEntities(query);
        }),
      )
      .subscribe({
        next: (response) => {
          this.results.set(response.data);
          this.totalResults.set(response.total);
          this.loading.set(false);
        },
        error: () => {
          this.loading.set(false);
        },
      });
    this.subscriptions.add(sub);
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
  }

  onSearchInput(value: string): void {
    this.searchQuery.set(value);
    this.searchSubject.next(value);
  }

  toggleEntityType(type: EntityType): void {
    const current = new Set(this.selectedEntityTypes());
    if (current.has(type)) {
      current.delete(type);
    } else {
      current.add(type);
    }
    this.selectedEntityTypes.set(current);
    this.applyFilters();
  }

  toggleSource(source: EntitySource): void {
    const current = new Set(this.selectedSources());
    if (current.has(source)) {
      current.delete(source);
    } else {
      current.add(source);
    }
    this.selectedSources.set(current);
    this.applyFilters();
  }

  setClassification(classification: Classification | null): void {
    this.selectedClassification.set(classification);
    this.applyFilters();
  }

  toggleGeoBounded(): void {
    this.geoBounded.set(!this.geoBounded());
    this.applyFilters();
  }

  clearFilters(): void {
    this.selectedEntityTypes.set(new Set());
    this.selectedSources.set(new Set());
    this.selectedClassification.set(null);
    this.geoBounded.set(false);
    this.applyFilters();
  }

  hasActiveFilters(): boolean {
    return (
      this.selectedEntityTypes().size > 0 ||
      this.selectedSources().size > 0 ||
      this.selectedClassification() !== null ||
      this.geoBounded()
    );
  }

  selectEntity(entity: Entity): void {
    // TODO: Fly to entity on map
    console.log('Selected entity:', entity.id, entity.name);
  }

  getEntityTypeColor(type: EntityType): string {
    const colorMap: Record<string, string> = {
      PERSON: 'var(--accent-blue)',
      VEHICLE: 'var(--accent-green)',
      VESSEL: 'var(--accent-cyan)',
      AIRCRAFT: 'var(--accent-yellow)',
      FACILITY: 'var(--accent-red)',
      SIGNAL: 'var(--accent-purple)',
      CYBER: 'var(--accent-purple)',
      EQUIPMENT: '#9ca3af',
      UNIT: 'var(--accent-green)',
      UNKNOWN: 'var(--text-muted)',
    };
    return colorMap[type] ?? 'var(--text-muted)';
  }

  private applyFilters(): void {
    // Re-trigger search with current query
    const query = this.searchQuery();
    if (query && query.trim().length >= 2) {
      this.loading.set(true);
      this.entityService.getEntities({
        search: query,
        entityType: this.selectedEntityTypes().size === 1
          ? [...this.selectedEntityTypes()][0]
          : undefined,
        source: this.selectedSources().size === 1
          ? [...this.selectedSources()][0]
          : undefined,
        classification: this.selectedClassification() ?? undefined,
        limit: 50,
      }).subscribe({
        next: (response) => {
          this.results.set(response.data);
          this.totalResults.set(response.total);
          this.loading.set(false);
        },
        error: () => this.loading.set(false),
      });
    }
  }
}
