import { Component, OnInit, OnDestroy, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { IconComponent } from '../../shared/components/icon.component';
import { Subject, Subscription, debounceTime, distinctUntilChanged, switchMap, of } from 'rxjs';
import {
  Entity,
  EntityType,
  EntitySource,
  Classification,
  PaginatedResponse,
} from '../../shared/models/entity.model';
import { EntityService } from '../../core/services/entity.service';
import { EntityFocusService } from '../../core/services/entity-focus.service';
import { MapComponent } from '../map/map.component';

@Component({
  selector: 'app-search',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule, IconComponent],
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

  constructor(
    private readonly entityService: EntityService,
    private readonly entityFocus: EntityFocusService,
  ) {}

  ngOnInit(): void {
    const sub = this.searchSubject
      .pipe(
        debounceTime(300),
        distinctUntilChanged(),
        switchMap((query) => {
          if (!query || query.trim().length < 2) {
            return of({ data: [], total: 0 } as PaginatedResponse<Entity>);
          }
          this.loading.set(true);
          return this.entityService.searchEntities(query);
        }),
      )
      .subscribe({
        next: (response) => {
          this.results.set(response.data);
          this.totalResults.set(response.pagination?.total ?? response.total ?? 0);
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

  /** Selects the entity on the map (the panel sits over it) and flies there. */
  selectEntity(entity: Entity): void {
    const p = entity.position;
    this.entityFocus.focus({
      entityId: entity.id,
      position: p ? { latitude: p.latitude, longitude: p.longitude } : undefined,
      label: entity.name,
    });
  }

  /** Enter on a result card selects it. */
  onCardKey(entity: Entity, event: Event): void {
    if (event.target !== event.currentTarget) return;
    event.preventDefault();
    this.selectEntity(entity);
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
      EQUIPMENT: 'var(--entity-equipment)',
      UNIT: 'var(--accent-green)',
      UNKNOWN: 'var(--entity-unknown)',
    };
    return colorMap[type] ?? 'var(--text-muted)';
  }

  private applyFilters(): void {
    // Re-trigger search with current query
    const query = this.searchQuery();
    if (query && query.trim().length >= 2) {
      this.loading.set(true);
      this.entityService.getEntities({
        entityTypes: this.selectedEntityTypes().size > 0
          ? [...this.selectedEntityTypes()]
          : undefined,
        sources: this.selectedSources().size > 0
          ? [...this.selectedSources()]
          : undefined,
        classification: this.selectedClassification() ?? undefined,
        pageSize: 50,
      }).subscribe({
        next: (response) => {
          this.results.set(response.data);
          this.totalResults.set(response.pagination?.total ?? response.total ?? 0);
          this.loading.set(false);
        },
        error: () => this.loading.set(false),
      });
    }
  }
}
