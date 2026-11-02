import { ChangeDetectionStrategy, Component, DestroyRef, OnInit, computed, inject, input, output, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { toObservable } from '@angular/core/rxjs-interop';
import { catchError, debounceTime, map, of, switchMap } from 'rxjs';
import { SearchService } from '../../core/services/search.service';
import { EntityType } from '../../shared/models/entity.model';
import { SearchHit } from '../../shared/models/search.model';
import { IconComponent } from '../../shared/components/icon.component';

const MIN_QUERY_LENGTH = 2;
const DEBOUNCE_MS = 300;
const MAX_HITS = 8;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * Picks an entity by name or identifier through the search index. Enter on
 * a pasted entity id picks it directly, for ids copied from elsewhere.
 */
@Component({
  selector: 'app-entity-picker',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, IconComponent],
  template: `
    <div class="picker">
      <div class="picker-input-wrap">
        <span class="picker-icon"><app-icon name="search" [size]="14" /></span>
        <input
          type="text"
          class="picker-input"
          [placeholder]="placeholder()"
          [value]="query()"
          [attr.aria-label]="placeholder()"
          (input)="onInput($any($event.target).value)"
          (keydown.enter)="onEnter()"
          (keydown.escape)="hits.set([])"
        />
        @if (query()) {
          <button type="button" class="picker-clear" (click)="clear()" aria-label="Clear"><app-icon name="x" [size]="12" /></button>
        }
      </div>
      @if (hits().length > 0) {
        <ul class="picker-hits" role="listbox">
          @for (hit of hits(); track hit.id) {
            <li>
              <button type="button" class="picker-hit" role="option" (click)="pick(hit)">
                <span class="picker-hit-name">{{ hit.name }}</span>
                <span class="chip chip-entity-type" [ngClass]="hit.entityType">{{ hit.entityType }}</span>
              </button>
            </li>
          }
        </ul>
      } @else if (searched() && !searching()) {
        <p class="picker-empty text-xs text-muted">No entity matches "{{ query() }}".</p>
      }
    </div>
  `,
  styles: [
    `
      .picker { position: relative; }
      .picker-input-wrap { position: relative; display: flex; align-items: center; }
      .picker-icon { position: absolute; left: 10px; display: flex; color: var(--text-muted); pointer-events: none; }
      .picker-input {
        width: 100%;
        padding: 8px 28px 8px 30px;
        background: var(--bg-primary);
        border: 1px solid var(--border-color);
        border-radius: var(--radius-sm);
        color: var(--text-primary);
        font-size: 0.85rem;
      }
      .picker-input:focus { border-color: var(--accent-blue); box-shadow: 0 0 0 2px rgba(59, 130, 246, 0.15); outline: none; }
      .picker-clear { position: absolute; right: 6px; display: flex; padding: 4px; background: none; border: 0; color: var(--text-muted); cursor: pointer; }
      .picker-clear:hover { color: var(--text-primary); }
      .picker-hits {
        position: absolute;
        z-index: 5;
        left: 0;
        right: 0;
        margin: 4px 0 0;
        padding: 4px;
        list-style: none;
        background: var(--bg-panel);
        backdrop-filter: blur(16px);
        border: 1px solid var(--border-color);
        border-radius: var(--radius-sm);
        box-shadow: var(--shadow-md);
        max-height: 240px;
        overflow-y: auto;
      }
      .picker-hit {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 8px;
        width: 100%;
        padding: 6px 8px;
        background: none;
        border: 0;
        border-radius: var(--radius-sm);
        color: var(--text-primary);
        font: inherit;
        text-align: left;
        cursor: pointer;
      }
      .picker-hit:hover, .picker-hit:focus-visible { background: var(--bg-panel-hover); outline: none; }
      .picker-hit-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
      .picker-empty { margin: 4px 0 0; }
    `,
  ],
})
export class EntityPickerComponent implements OnInit {
  private readonly search = inject(SearchService);
  private readonly destroyRef = inject(DestroyRef);

  placeholder = input<string>('Find an entity by name or identifier');
  /** The chosen entity, or a bare id the user pasted (name is the id then). */
  picked = output<SearchHit>();

  query = signal<string>('');
  hits = signal<SearchHit[]>([]);
  searching = signal<boolean>(false);
  /** A search ran for the current query, so "no matches" is meaningful. */
  searched = signal<boolean>(false);

  private readonly term = computed(() => {
    const q = this.query().trim();
    return q.length >= MIN_QUERY_LENGTH && !UUID.test(q) ? q : null;
  });

  private readonly terms$ = toObservable(this.term);

  ngOnInit(): void {
    const sub = this.terms$
      .pipe(
        debounceTime(DEBOUNCE_MS),
        switchMap((q) => {
          if (!q) return of<SearchHit[] | null>(null);
          this.searching.set(true);
          return this.search.search({ q, pageSize: MAX_HITS }).pipe(
            map((res) => res.hits),
            catchError(() => of([] as SearchHit[])),
          );
        }),
      )
      .subscribe((hits) => {
        this.searching.set(false);
        this.searched.set(hits !== null);
        this.hits.set(hits ?? []);
      });
    this.destroyRef.onDestroy(() => sub.unsubscribe());
  }

  onInput(value: string): void {
    this.query.set(value);
  }

  /** Enter picks the only hit, or a pasted entity id as it stands. */
  onEnter(): void {
    const q = this.query().trim();
    const hits = this.hits();
    if (hits.length === 1) this.pick(hits[0]);
    else if (UUID.test(q)) this.pick({ id: q, name: q, entityType: EntityType.UNKNOWN });
  }

  pick(hit: SearchHit): void {
    this.query.set(hit.name);
    this.hits.set([]);
    this.searched.set(false);
    this.picked.emit(hit);
  }

  clear(): void {
    this.query.set('');
    this.hits.set([]);
    this.searched.set(false);
  }
}
