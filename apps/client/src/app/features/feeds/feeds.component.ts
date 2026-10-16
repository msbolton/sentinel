import { ChangeDetectionStrategy, Component, OnDestroy, OnInit, computed, inject, signal } from '@angular/core';
import { DecimalPipe } from '@angular/common';
import { FeedService, problemsOf } from '../../core/services/feed.service';
import { Feed, FeedHealthLevel, FeedState } from '../../shared/models/feed.model';
import { FeedFormComponent } from './feed-form.component';

export type DrawerState = { mode: 'create' } | { mode: 'edit'; feed: Feed };

interface FeedRow {
  feed: Feed;
  /** What the toggle shows: the optimistic value while a change is in flight. */
  enabled: boolean;
  driverName: string;
  highlights: string[];
  detailEntries: Array<{ key: string; value: string }>;
}

const STATE_LABELS: Record<FeedState, string> = {
  stopped: 'Stopped',
  starting: 'Starting',
  running: 'Running',
  idle: 'Idle',
  degraded: 'Degraded',
  error: 'Error',
  unassigned: 'Unassigned',
};

const HEALTH_LABELS: Record<FeedHealthLevel, string> = {
  healthy: 'Healthy',
  warn: 'Stale (warning)',
  critical: 'Stale (critical)',
  unknown: 'Unknown',
};

/**
 * Operator page for ingest feeds: lists every feed with its runtime
 * status, toggles them, and opens the drawer to create or edit one.
 */
@Component({
  selector: 'app-feeds',
  standalone: true,
  imports: [DecimalPipe, FeedFormComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './feeds.component.html',
  styleUrl: './feeds.component.scss',
})
export class FeedsComponent implements OnInit, OnDestroy {
  private readonly feedService = inject(FeedService);

  readonly loading = this.feedService.loading;
  readonly loadError = this.feedService.error;
  readonly actionError = signal<string | null>(null);
  readonly driversError = signal<string | null>(null);

  readonly now = signal(Date.now());
  readonly expanded = signal<ReadonlySet<string>>(new Set());
  readonly confirmingDelete = signal<string | null>(null);
  readonly deleting = signal<string | null>(null);
  readonly drawer = signal<DrawerState | null>(null);
  private readonly pendingEnabled = signal<Record<string, boolean>>({});

  readonly hasLoaded = computed(() => this.feedService.hasLoaded || this.feedService.feeds().length > 0);

  readonly rows = computed<FeedRow[]>(() => {
    const pending = this.pendingEnabled();
    const drivers = this.feedService.drivers();
    return this.feedService.feeds().map((feed) => ({
      feed,
      enabled: pending[feed.id] ?? feed.enabled,
      driverName: drivers.find((d) => d.id === feed.driver)?.name ?? feed.driver,
      highlights: highlightsOf(feed.status.detail),
      detailEntries: Object.entries(feed.status.detail ?? {})
        .filter(([key]) => key !== 'leaseToken')
        .map(([key, value]) => ({ key, value: formatDetail(value) })),
    }));
  });

  private release: (() => void) | null = null;
  private clock: ReturnType<typeof setInterval> | null = null;

  ngOnInit(): void {
    this.release = this.feedService.poll(10_000);
    this.feedService.loadDrivers().subscribe({
      next: () => this.driversError.set(null),
      error: (err) => this.driversError.set(`Driver catalog unavailable: ${problemsOf(err).message}`),
    });
    this.clock = setInterval(() => this.now.set(Date.now()), 10_000);
  }

  ngOnDestroy(): void {
    this.release?.();
    this.release = null;
    if (this.clock) {
      clearInterval(this.clock);
      this.clock = null;
    }
  }

  // --- presentation -----------------------------------------------------

  stateLabel(state: FeedState): string {
    return STATE_LABELS[state] ?? state;
  }

  healthLabel(health: FeedHealthLevel): string {
    return HEALTH_LABELS[health] ?? health;
  }

  /** "12s ago", "3m ago", … relative to the page clock (refreshed with each poll). */
  ago(iso: string | null | undefined): string {
    if (!iso) {
      return 'never';
    }
    const t = Date.parse(iso);
    if (Number.isNaN(t)) {
      return 'unknown';
    }
    const seconds = Math.max(0, Math.round((this.now() - t) / 1000));
    if (seconds < 60) {
      return `${seconds}s ago`;
    }
    const minutes = Math.round(seconds / 60);
    if (minutes < 60) {
      return `${minutes}m ago`;
    }
    const hours = Math.round(minutes / 60);
    if (hours < 48) {
      return `${hours}h ago`;
    }
    return `${Math.round(hours / 24)}d ago`;
  }

  isExpanded(id: string): boolean {
    return this.expanded().has(id);
  }

  toggleExpanded(id: string): void {
    this.expanded.update((set) => {
      const next = new Set(set);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  // --- actions ----------------------------------------------------------

  retry(): void {
    this.feedService.load().subscribe({ error: () => undefined });
  }

  dismissActionError(): void {
    this.actionError.set(null);
  }

  openCreate(): void {
    this.drawer.set({ mode: 'create' });
  }

  openEdit(feed: Feed): void {
    this.drawer.set({ mode: 'edit', feed });
  }

  closeDrawer(): void {
    this.drawer.set(null);
  }

  toggleEnabled(feed: Feed, enabled: boolean): void {
    if (enabled === (this.pendingEnabled()[feed.id] ?? feed.enabled)) {
      return;
    }
    this.actionError.set(null);
    this.pendingEnabled.update((m) => ({ ...m, [feed.id]: enabled }));
    this.feedService.setEnabled(feed.id, enabled).subscribe({
      next: () => this.clearPending(feed.id),
      error: (err) => {
        this.clearPending(feed.id);
        this.actionError.set(`Could not ${enabled ? 'enable' : 'disable'} ${feed.name}: ${problemsOf(err).message}`);
      },
    });
  }

  askDelete(feed: Feed): void {
    if (feed.builtin) {
      return;
    }
    this.confirmingDelete.set(feed.id);
  }

  cancelDelete(): void {
    this.confirmingDelete.set(null);
  }

  confirmDelete(feed: Feed): void {
    this.actionError.set(null);
    this.deleting.set(feed.id);
    this.feedService.delete(feed.id).subscribe({
      next: () => {
        this.deleting.set(null);
        this.confirmingDelete.set(null);
      },
      error: (err) => {
        this.deleting.set(null);
        this.confirmingDelete.set(null);
        this.actionError.set(`Could not delete ${feed.name}: ${problemsOf(err).message}`);
      },
    });
  }

  private clearPending(id: string): void {
    this.pendingEnabled.update((m) => {
      if (!(id in m)) {
        return m;
      }
      const next = { ...m };
      delete next[id];
      return next;
    });
  }
}

/** The driver detail values worth a glance in the row. */
function highlightsOf(detail: Record<string, unknown> | undefined): string[] {
  if (!detail) {
    return [];
  }
  const out: string[] = [];
  if (detail['idle'] === true) {
    out.push('idle');
  }
  if (typeof detail['creditsRemaining'] === 'number') {
    out.push(`${detail['creditsRemaining']} credits`);
  }
  if (typeof detail['effectiveIntervalSec'] === 'number') {
    out.push(`every ${detail['effectiveIntervalSec']}s`);
  }
  if (typeof detail['connections'] === 'number') {
    out.push(`${detail['connections']} conn`);
  }
  return out;
}

function formatDetail(value: unknown): string {
  if (value === null || value === undefined) {
    return '—';
  }
  if (typeof value === 'object') {
    return JSON.stringify(value);
  }
  return String(value);
}
