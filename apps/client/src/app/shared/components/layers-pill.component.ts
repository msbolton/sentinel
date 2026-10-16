import {
  Component,
  ChangeDetectionStrategy,
  signal,
  computed,
  inject,
  HostListener,
  ElementRef,
  OnInit,
  OnDestroy,
} from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { map, throttleTime } from 'rxjs';
import { FeedService } from '../../core/services/feed.service';
import { LayerVisibilityService } from '../../core/services/layer-visibility.service';
import { EntityService } from '../../core/services/entity.service';
import { Feed, directionOf, layerKeyOf } from '../../shared/models/feed.model';
import { HasRoleDirective } from '../directives/has-role.directive';

type Freshness = 'green' | 'yellow' | 'red' | 'gray' | 'none';

interface LayerRow {
  /** layerKeyOf(entity): the feed id, else the legacy source. */
  key: string;
  name: string;
  /** Null for a layer seen on the map that no feed claims. */
  feed: Feed | null;
  count: number;
  hidden: boolean;
  freshness: Freshness;
  meta: string;
}

/**
 * The "DATA LAYERS" pill on the map: one row per feed (built-ins first) and
 * one per extra layer key present on the map. The eye toggle is this user's
 * own visibility (LayerVisibilityService); feed health and counts are read
 * only. Enabling, editing and deleting feeds live on the /connections page.
 * Connections that only send (`out`) produce no entities and get no row.
 */
@Component({
  selector: 'app-layers-pill',
  standalone: true,
  imports: [RouterLink, HasRoleDirective],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="pill-container" [class.expanded]="expanded()">
      <!-- Pill / Header bar -->
      <button class="pill-header" (click)="toggle()">
        <span class="pill-label">DATA LAYERS</span>
        @if (!expanded() && visibleCount() > 0) {
          <span class="feed-badge">{{ visibleCount() }}</span>
        }
        <span class="pill-rule" [class.visible]="expanded()"></span>
        <span class="pill-toggle-btn">{{ expanded() ? '−' : '+' }}</span>
      </button>

      <!-- Expanded panel -->
      @if (expanded()) {
        <div class="pill-panel">
          @for (layer of layers(); track layer.key) {
            <div class="layer-row" [class.hidden]="layer.hidden">
              <button
                class="layer-eye"
                type="button"
                [class.off]="layer.hidden"
                [attr.aria-pressed]="!layer.hidden"
                [attr.aria-label]="(layer.hidden ? 'Show ' : 'Hide ') + layer.name"
                [title]="layer.hidden ? 'Show on my map' : 'Hide on my map'"
                (click)="toggleVisibility(layer); $event.stopPropagation()">
                @if (layer.hidden) {
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/>
                    <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/>
                    <path d="M14.12 14.12a3 3 0 1 1-4.24-4.24"/>
                    <path d="M1 1l22 22"/>
                  </svg>
                } @else {
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>
                    <circle cx="12" cy="12" r="3"/>
                  </svg>
                }
              </button>
              <div class="layer-content">
                <div class="layer-main">
                  <span class="freshness-dot" [attr.data-status]="layer.freshness"></span>
                  <span class="layer-name">{{ layer.name }}</span>
                  <span class="layer-count">{{ formatCount(layer.count) }}</span>
                </div>
                <div class="layer-meta">{{ layer.meta }}</div>
              </div>
            </div>
          }
          @if (layers().length === 0) {
            <div class="layer-empty">
              @if (feedService.hasLoaded) {
                No layers configured
              } @else if (feedService.error()) {
                Ingest service offline
              } @else {
                Loading…
              }
            </div>
          }
          <a
            *appHasRole="['sentinel-operator', 'sentinel-admin']"
            class="manage-link"
            routerLink="/connections"
            (click)="collapse()">MANAGE CONNECTIONS →</a>
        </div>
      }
    </div>
  `,
  styles: [`
    :host {
      display: block;
    }

    .pill-container {
      display: inline-flex;
      flex-direction: column;
    }

    .pill-container.expanded {
      width: 340px;
    }

    .pill-header {
      display: inline-flex;
      align-items: center;
      gap: 10px;
      padding: 10px 18px;
      min-width: 200px;
      border-radius: 24px;
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      cursor: pointer;
      font-family: var(--font-mono);
      font-size: 0.75rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--text-secondary);
      white-space: nowrap;
      transition: color var(--transition-fast), border-color var(--transition-fast);

      &:hover {
        color: var(--text-primary);
        border-color: color-mix(in srgb, var(--border-color) 100%, var(--text-muted) 30%);
      }
    }

    .pill-container.expanded .pill-header {
      width: 100%;
      border-radius: var(--radius-lg) var(--radius-lg) 0 0;
    }

    .pill-rule {
      flex: 1;
      height: 1px;

      &.visible {
        background: var(--border-color);
      }
    }

    .pill-toggle-btn {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 20px;
      height: 20px;
      border-radius: 4px;
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid var(--border-color);
      font-size: 0.85rem;
      line-height: 1;
      flex-shrink: 0;
      color: var(--text-muted);
    }

    .feed-badge {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-width: 18px;
      height: 18px;
      padding: 0 5px;
      border-radius: 9px;
      background: var(--accent-green);
      color: #000;
      font-size: 0.65rem;
      font-weight: 700;
      line-height: 1;
    }

    .pill-panel {
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      border-top: none;
      border-radius: 0 0 var(--radius-lg) var(--radius-lg);
      padding: 6px;
      animation: floatIn 200ms cubic-bezier(0.16, 1, 0.3, 1);
    }

    .layer-row {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 8px 10px;
      border-radius: var(--radius-sm);
      transition: background var(--transition-fast), opacity var(--transition-fast);

      &:hover {
        background: color-mix(in srgb, var(--text-muted) 8%, transparent);
      }

      &.hidden .layer-content {
        opacity: 0.45;
      }
    }

    .layer-eye {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 24px;
      height: 24px;
      border-radius: 4px;
      background: rgba(6, 182, 212, 0.1);
      border: 1px solid var(--accent-cyan, var(--accent-blue));
      color: var(--accent-cyan, var(--accent-blue));
      cursor: pointer;
      flex-shrink: 0;
      transition: all 150ms ease;

      &.off {
        background: rgba(255, 255, 255, 0.05);
        border-color: var(--border-color);
        color: var(--text-muted);
      }

      &:hover {
        color: var(--text-primary);
        border-color: var(--text-primary);
      }
    }

    .layer-content {
      display: flex;
      flex-direction: column;
      gap: 3px;
      min-width: 0;
      width: 100%;
      transition: opacity var(--transition-fast);
    }

    .layer-main {
      display: flex;
      align-items: center;
      gap: 8px;
    }

    .freshness-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      flex-shrink: 0;

      &[data-status="green"]  { background: var(--accent-green); }
      &[data-status="yellow"] { background: #eab308; }
      &[data-status="red"]    { background: #ef4444; }
      &[data-status="gray"]   { background: #6b7280; }
      &[data-status="none"]   { background: transparent; }
    }

    .layer-name {
      font-size: 0.8rem;
      font-weight: 500;
      color: var(--text-primary);
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
      flex: 1;
      min-width: 0;
    }

    .layer-count {
      font-family: var(--font-mono);
      font-size: 0.7rem;
      color: var(--text-muted);
      white-space: nowrap;
      min-width: 32px;
      text-align: right;
    }

    .layer-meta {
      font-family: var(--font-mono);
      font-size: 0.65rem;
      color: var(--text-muted);
      letter-spacing: 0.02em;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .layer-empty {
      padding: 12px 10px;
      font-size: 0.75rem;
      color: var(--text-muted);
      text-align: center;
    }

    .manage-link {
      display: block;
      width: 100%;
      padding: 8px 10px;
      margin-top: 4px;
      border-radius: var(--radius-sm);
      font-family: var(--font-mono);
      font-size: 0.7rem;
      font-weight: 600;
      letter-spacing: 0.08em;
      text-align: center;
      text-decoration: none;
      background: rgba(6, 182, 212, 0.08);
      color: var(--accent-cyan, var(--accent-blue));
      border: 1px dashed color-mix(in srgb, var(--accent-cyan, var(--accent-blue)) 40%, transparent);
      cursor: pointer;
      transition: all 150ms ease;

      &:hover {
        background: rgba(6, 182, 212, 0.18);
        border-color: var(--accent-cyan, var(--accent-blue));
      }
    }

    @keyframes floatIn {
      from {
        opacity: 0;
        transform: translateY(-4px);
      }
      to {
        opacity: 1;
        transform: translateY(0);
      }
    }
  `],
})
export class LayersPillComponent implements OnInit, OnDestroy {
  readonly feedService = inject(FeedService);
  private readonly layerVisibility = inject(LayerVisibilityService);
  private readonly entityService = inject(EntityService);
  private readonly elementRef = inject(ElementRef);

  readonly expanded = signal(false);
  private releasePoll: (() => void) | null = null;

  /** Entities on the map per layer key, refreshed at most once a second. */
  private readonly countsByKey = toSignal(
    this.entityService.currentEntities$.pipe(
      throttleTime(1000, undefined, { leading: true, trailing: true }),
      map((entities) => {
        const counts = new Map<string, number>();
        entities.forEach((entity) => {
          const key = layerKeyOf(entity);
          counts.set(key, (counts.get(key) ?? 0) + 1);
        });
        return counts;
      }),
    ),
    { initialValue: new Map<string, number>() },
  );

  readonly layers = computed<LayerRow[]>(() => {
    const feeds = this.feedService.feeds().filter((feed) => directionOf(feed) !== 'out');
    const counts = this.countsByKey();
    const hidden = this.layerVisibility.hidden();

    const rows: LayerRow[] = feeds.map((feed) => ({
      key: feed.id,
      name: feed.name,
      feed,
      count: counts.get(feed.id) ?? 0,
      hidden: hidden.has(feed.id),
      freshness: this.healthToFreshness(feed),
      meta: this.describe(feed),
    }));

    // Layers on the map that no feed claims (legacy sources, unknown).
    const feedKeys = new Set(feeds.map((f) => f.id));
    const extras = [...counts.keys()].filter((key) => !feedKeys.has(key)).sort();
    for (const key of extras) {
      rows.push({
        key,
        name: key,
        feed: null,
        count: counts.get(key) ?? 0,
        hidden: hidden.has(key),
        freshness: 'none',
        meta: 'map only',
      });
    }

    return rows;
  });

  /** Rows this user has not hidden; shown on the collapsed badge. */
  readonly visibleCount = computed(() => this.layers().filter((l) => !l.hidden).length);

  ngOnInit(): void {
    if (!this.feedService.hasLoaded) {
      this.feedService.load().subscribe({ error: () => { /* surfaced via feedService.error() */ } });
    }
  }

  ngOnDestroy(): void {
    this.stopPolling();
  }

  toggle(): void {
    if (this.expanded()) {
      this.collapse();
    } else {
      this.expanded.set(true);
      this.releasePoll ??= this.feedService.poll(15_000);
    }
  }

  collapse(): void {
    this.expanded.set(false);
    this.stopPolling();
  }

  /** This user's map only; the feed itself keeps running for everyone. */
  toggleVisibility(layer: LayerRow): void {
    this.layerVisibility.toggle(layer.key);
  }

  formatCount(count: number): string {
    if (count >= 1000) {
      const k = count / 1000;
      return k % 1 === 0 ? `${k}K` : `${k.toFixed(1)}K`;
    }
    return String(count);
  }

  @HostListener('document:click', ['$event'])
  onDocumentClick(event: MouseEvent): void {
    if (this.expanded() && !this.elementRef.nativeElement.contains(event.target)) {
      this.collapse();
    }
  }

  private stopPolling(): void {
    this.releasePoll?.();
    this.releasePoll = null;
  }

  private healthToFreshness(feed: Feed): Freshness {
    if (!feed.enabled) return 'gray';
    switch (feed.status?.health) {
      case 'healthy': return 'green';
      case 'warn': return 'yellow';
      case 'critical': return 'red';
      default: return 'gray';
    }
  }

  private describe(feed: Feed): string {
    if (!feed.enabled) {
      return `${feed.source} · disabled`;
    }
    const parts: string[] = [feed.status?.state ?? 'unknown'];
    if (feed.status?.owner) parts.push(feed.status.owner);
    parts.push(this.getRelativeTime(feed));
    return parts.join(' · ');
  }

  private getRelativeTime(feed: Feed): string {
    if (!feed.status?.lastSuccessAt) {
      return feed.enabled ? 'waiting...' : 'never';
    }
    const lastSeen = new Date(feed.status.lastSuccessAt);
    const ageSec = Math.floor((Date.now() - lastSeen.getTime()) / 1000);
    if (ageSec < 5) return 'just now';
    if (ageSec < 60) return `${ageSec}s ago`;
    if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`;
    return `${Math.floor(ageSec / 3600)}h ago`;
  }
}
