import { ChangeDetectionStrategy, Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { toObservable } from '@angular/core/rxjs-interop';
import { Subject, Subscription, catchError, debounceTime, map, of, switchMap } from 'rxjs';
import { IconComponent } from '../../shared/components/icon.component';
import { Classification, Entity, EntitySource, EntityType } from '../../shared/models/entity.model';
import { SearchHit, SearchRequest, hitToEntity } from '../../shared/models/search.model';
import { EntityService } from '../../core/services/entity.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { SearchService } from '../../core/services/search.service';
import { SelectionService } from '../../core/services/selection.service';

/** Shorter queries are not sent; the index needs two characters to match anything. */
const MIN_QUERY_LENGTH = 2;
const DEBOUNCE_MS = 300;
export const RESULTS_PAGE_SIZE = 50;

/**
 * Full-text search over the track index. The query, filters and the viewport
 * toggle feed one request; clicking a hit selects the track on the map, from
 * the live cache when it is there, else fetched, else by flying to where the
 * index last saw it.
 */
@Component({
  selector: 'app-search',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, IconComponent],
  templateUrl: './search.component.html',
  styleUrls: ['./search.component.scss'],
})
export class SearchComponent implements OnInit {
  private readonly search = inject(SearchService);
  private readonly entities = inject(EntityService);
  private readonly selection = inject(SelectionService);
  private readonly viewport = inject(MapViewportService);
  private readonly destroyRef = inject(DestroyRef);

  searchQuery = signal<string>('');
  results = signal<Entity[]>([]);
  totalResults = signal<number>(0);
  loading = signal<boolean>(false);
  /** The last request failed; the empty state says so instead of "no matches". */
  failed = signal<boolean>(false);
  selectedEntityTypes = signal<Set<EntityType>>(new Set());
  selectedSources = signal<Set<EntitySource>>(new Set());
  selectedClassification = signal<Classification | null>(null);
  geoBounded = signal<boolean>(false);

  entityTypes = Object.values(EntityType);
  entitySources = Object.values(EntitySource);
  classifications = Object.values(Classification);

  /** The request the current inputs describe, or null while the query is too short. */
  readonly request = computed<SearchRequest | null>(() => {
    const q = this.searchQuery().trim();
    if (q.length < MIN_QUERY_LENGTH) return null;
    const types = [...this.selectedEntityTypes()];
    const sources = [...this.selectedSources()];
    const cls = this.selectedClassification();
    const bounds = this.geoBounded() ? this.viewport.bounds() : null;
    return {
      q,
      ...(types.length ? { types } : {}),
      ...(sources.length ? { sources } : {}),
      ...(cls ? { classifications: [cls] } : {}),
      ...(bounds ? { bounds } : {}),
      pageSize: RESULTS_PAGE_SIZE,
    };
  });

  /** Typing re-arms the debounce; filter and toggle changes go through the same pipe. */
  private readonly requests$ = toObservable(this.request);
  private readonly selectRequests = new Subject<Entity>();
  private readonly subscriptions = new Subscription();

  ngOnInit(): void {
    this.subscriptions.add(
      this.requests$
        .pipe(
          debounceTime(DEBOUNCE_MS),
          switchMap((req) => {
            if (!req) return of({ hits: [] as SearchHit[], total: 0, failed: false });
            this.loading.set(true);
            return this.search.search(req).pipe(
              map((res) => ({ hits: res.hits, total: res.total, failed: false })),
              catchError(() => of({ hits: [] as SearchHit[], total: 0, failed: true })),
            );
          }),
        )
        .subscribe(({ hits, total, failed }) => {
          this.results.set(hits.map(hitToEntity));
          this.totalResults.set(total);
          this.failed.set(failed);
          this.loading.set(false);
        }),
    );
    this.destroyRef.onDestroy(() => this.subscriptions.unsubscribe());
  }

  onSearchInput(value: string): void {
    this.searchQuery.set(value);
  }

  toggleEntityType(type: EntityType): void {
    this.selectedEntityTypes.update((current) => toggled(current, type));
  }

  toggleSource(source: EntitySource): void {
    this.selectedSources.update((current) => toggled(current, source));
  }

  setClassification(classification: Classification | null): void {
    this.selectedClassification.set(classification);
  }

  toggleGeoBounded(): void {
    this.geoBounded.update((on) => !on);
  }

  clearFilters(): void {
    this.selectedEntityTypes.set(new Set());
    this.selectedSources.set(new Set());
    this.selectedClassification.set(null);
    this.geoBounded.set(false);
  }

  hasActiveFilters(): boolean {
    return (
      this.selectedEntityTypes().size > 0 ||
      this.selectedSources().size > 0 ||
      this.selectedClassification() !== null ||
      this.geoBounded()
    );
  }

  /**
   * Selects the hit's track: the live one when the map has it, else the
   * entity service's copy, else the indexed position (the track may have
   * aged out of the live set but still be in the index).
   */
  selectEntity(entity: Entity): void {
    if (this.selection.selectById(entity.id)) return;
    const fallback = () => this.selection.select(entity);
    this.subscriptions.add(
      this.entities.getEntity(entity.id).subscribe({
        next: (live) => this.selection.select(live),
        error: fallback,
      }),
    );
  }

  isSelected(entity: Entity): boolean {
    return this.selection.entity()?.id === entity.id;
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
}

function toggled<T>(current: Set<T>, value: T): Set<T> {
  const next = new Set(current);
  if (next.has(value)) next.delete(value);
  else next.add(value);
  return next;
}
