import { ChangeDetectionStrategy, Component, DestroyRef, computed, inject, linkedSignal, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { map } from 'rxjs';
import { EntityService } from '../../core/services/entity.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { SelectionService } from '../../core/services/selection.service';
import { freshnessOf } from '../freshness';
import { Affiliation, Entity } from '../models/entity.model';
import { affiliationAbbrev, freshestInView } from '../track-stack';

const TICK_MS = 5000;
/** How long the order stays frozen after the pointer leaves, so crossing the gap between rows does not reshuffle them. */
const UNFREEZE_MS = 250;

/**
 * A short stack of one-line rows down the right edge of the map: the ten
 * tracks in view that reported most recently, freshest first. Clicking a row
 * selects the track. Renders nothing while the list is empty; only the rows
 * take pointer events. The order holds still while the pointer is over the
 * stack, so a row does not move out from under a click.
 */
@Component({
  selector: 'app-track-stack',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  host: {
    '(mouseenter)': 'onPointerEnter()',
    '(mouseleave)': 'onPointerLeave()',
  },
  template: `
    @for (e of rows(); track e.id) {
      <button
        type="button"
        class="row"
        [class.h]="isHostile(e)"
        [class.on]="e.id === selection.entity()?.id"
        [attr.aria-pressed]="e.id === selection.entity()?.id"
        (click)="selection.select(e)">
        <span class="who">{{ who(e) }}</span>
        <span class="meta">{{ meta(e) }}</span>
      </button>
    }
  `,
  styles: [`
    :host {
      position: absolute;
      right: 16px;
      top: calc(92px + var(--track-card-height, 0px) + 6px);
      width: 300px;
      z-index: 60;
      display: flex;
      flex-direction: column;
      gap: 6px;
      pointer-events: none;
      /* Stop above the bottom-right readouts. */
      max-height: calc(100vh - 92px - var(--track-card-height, 0px) - 6px - 90px);
      overflow: hidden;
    }

    /* A route panel covers the right edge of the map; the stack moves clear of it. */
    :host(.panel-open) {
      right: calc(16px + var(--panel-width));
    }

    .row {
      pointer-events: auto;
      display: flex;
      justify-content: space-between;
      gap: 8px;
      font: 12px var(--font-mono);
      color: var(--text-primary);
      background: var(--bg-panel);
      border: 1px solid var(--border-color);
      border-radius: 0;
      padding: 6px 10px;
      text-align: left;
      cursor: pointer;
    }

    .row.h {
      border-color: var(--accent-red);
      color: #ffb4a5;
    }

    .row.on {
      border-color: #fff;
    }

    .row:focus-visible {
      outline: 1px solid var(--accent-cyan);
      outline-offset: 0;
    }

    .who {
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .meta {
      flex: none;
      color: var(--text-muted);
      white-space: nowrap;
    }
  `],
})
export class TrackStackComponent {
  readonly selection = inject(SelectionService);
  private readonly viewport = inject(MapViewportService);

  // currentEntities$ re-emits the same mutated Map, which toSignal would treat as unchanged;
  // copying the values gives every emission a new reference.
  private readonly entities = toSignal(
    inject(EntityService).currentEntities$.pipe(map((m) => [...m.values()])),
    { initialValue: [] as Entity[] },
  );
  private readonly now = signal(Date.now());

  /** The pointer is over the stack: the row order is held. */
  readonly hovered = signal(false);
  private unfreezeTimer: ReturnType<typeof setTimeout> | null = null;

  private readonly live = computed(() => freshestInView(this.entities(), this.viewport.bounds(), this.now()));

  /** The live rows, except while hovered, when the last order shown is kept. */
  readonly rows = linkedSignal<{ live: Entity[]; hovered: boolean }, Entity[]>({
    source: () => ({ live: this.live(), hovered: this.hovered() }),
    computation: (src, previous) => (src.hovered && previous ? previous.value : src.live),
  });

  constructor() {
    const timer = setInterval(() => this.now.set(Date.now()), TICK_MS);
    inject(DestroyRef).onDestroy(() => {
      clearInterval(timer);
      this.cancelUnfreeze();
    });
  }

  onPointerEnter(): void {
    this.cancelUnfreeze();
    this.hovered.set(true);
  }

  onPointerLeave(): void {
    this.cancelUnfreeze();
    this.unfreezeTimer = setTimeout(() => {
      this.unfreezeTimer = null;
      this.hovered.set(false);
    }, UNFREEZE_MS);
  }

  private cancelUnfreeze(): void {
    if (this.unfreezeTimer !== null) clearTimeout(this.unfreezeTimer);
    this.unfreezeTimer = null;
  }

  isHostile(e: Entity): boolean {
    return e.affiliation === Affiliation.HOSTILE || e.affiliation === Affiliation.SUSPECT;
  }

  who(e: Entity): string {
    return `${e.id.slice(0, 8)} ${e.name.toUpperCase()}`;
  }

  meta(e: Entity): string {
    return `${affiliationAbbrev(e.affiliation)} · ${freshnessOf(e, this.now()).label}`;
  }
}
