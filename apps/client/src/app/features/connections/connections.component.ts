import { ChangeDetectionStrategy, Component, OnDestroy, OnInit, computed, inject, signal } from '@angular/core';
import { DecimalPipe } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, Router } from '@angular/router';
import { Subscription } from 'rxjs';
import { EmplacementService } from '../../core/services/emplacement.service';
import { sitesSummaryOf } from '../../shared/models/emplacement.model';
import { ConnectionEmplacementsComponent } from './connection-emplacements.component';
import { FeedService, problemsOf } from '../../core/services/feed.service';
import {
  ConnectionPolicy,
  Feed,
  FeedDirection,
  FeedHealthLevel,
  FeedState,
  SinkDetail,
  directionOf,
  isSending,
  policySummaryOf,
  sinkDetailOf,
} from '../../shared/models/feed.model';
import { HasRoleDirective } from '../../shared/directives/has-role.directive';
import { ConnectionFormComponent } from './connection-form.component';
import { DeadLettersComponent } from './dead-letters.component';
import { IngestTokensComponent } from './ingest-tokens.component';
import { UnassignedEmplacementsComponent } from './unassigned-emplacements.component';
import { SendGuideComponent } from './send-guide.component';
import { endpointLabel, sendTarget } from './send-guide';
import { listenOf } from './listen-config';

export type DrawerState = { mode: 'create' } | { mode: 'edit'; feed: Feed };

interface FeedRow {
  feed: Feed;
  /** What the toggle shows: the optimistic value while a change is in flight. */
  enabled: boolean;
  driverName: string;
  direction: FeedDirection;
  /** Whether the connection sends entities out (out or both). */
  sending: boolean;
  /** One line for a sending connection's policy. */
  policySummary: string;
  /** What the Source column shows. */
  sourceLabel: string;
  /** Whether the connection listens on a port, whatever its driver. */
  listening: boolean;
  sink: SinkDetail;
  highlights: string[];
  detailEntries: Array<{ key: string; value: string; withheld: boolean }>;
}

/** Which connections the list shows. */
export type ConnectionFilter = 'all' | 'listening';

const STATE_LABELS: Record<FeedState, string> = {
  stopped: 'Stopped',
  starting: 'Starting',
  running: 'Running',
  idle: 'Idle',
  degraded: 'Degraded',
  error: 'Error',
  unassigned: 'Unassigned',
};

const DIRECTION_LABELS: Record<FeedDirection, string> = {
  in: 'Receives entities',
  out: 'Sends entities',
  both: 'Receives and sends',
};

const HEALTH_LABELS: Record<FeedHealthLevel, string> = {
  healthy: 'Healthy',
  // A connection with emplacements is warned when some are silent, not stale.
  warn: 'Warning',
  critical: 'Critical',
  unknown: 'Unknown',
};

/**
 * Operator page for connections (ingest feeds and outbound sinks): lists
 * every one with its runtime status, toggles them, and opens the drawer
 * to create or edit one.
 */
@Component({
  selector: 'app-connections',
  standalone: true,
  imports: [DecimalPipe, ConnectionEmplacementsComponent, ConnectionFormComponent, DeadLettersComponent, HasRoleDirective, IngestTokensComponent, SendGuideComponent, UnassignedEmplacementsComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './connections.component.html',
  styleUrl: './connections.component.scss',
})
export class ConnectionsComponent implements OnInit, OnDestroy {
  private readonly feedService = inject(FeedService);
  private readonly emplacements = inject(EmplacementService);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);

  /** Who sees a connection's recent rejects; the server refuses everyone else, so this only spares them the panel. */
  readonly deadLetterRoles = ['sentinel-operator', 'sentinel-admin'];

  readonly loading = this.feedService.loading;
  readonly loadError = this.feedService.error;
  readonly actionError = signal<string | null>(null);
  readonly driversError = signal<string | null>(null);

  readonly now = signal(Date.now());
  readonly expanded = signal<ReadonlySet<string>>(new Set());
  readonly confirmingDelete = signal<string | null>(null);
  readonly deleting = signal<string | null>(null);
  readonly drawer = signal<DrawerState | null>(null);
  readonly filter = signal<ConnectionFilter>('all');
  private readonly pendingEnabled = signal<Record<string, boolean>>({});

  readonly hasLoaded = computed(() => this.feedService.hasLoaded || this.feedService.feeds().length > 0);

  readonly rows = computed<FeedRow[]>(() => {
    const pending = this.pendingEnabled();
    const drivers = this.feedService.drivers();
    return this.feedService.feeds().map((feed) => {
      const sink = sinkDetailOf(feed.status?.detail);
      const policy = feed.config?.['policy'];
      return {
        feed,
        enabled: pending[feed.id] ?? feed.enabled,
        driverName: drivers.find((d) => d.id === feed.driver)?.name ?? feed.driver,
        direction: directionOf(feed),
        sending: isSending(feed),
        policySummary: policySummaryOf(policy && typeof policy === 'object' ? (policy as ConnectionPolicy) : undefined),
        sourceLabel: sourceLabelOf(feed),
        // A tcp connection in connect mode has the driver's "listen" capability but binds nothing.
        listening: listenOf(feed.driver, feed.config) !== null,
        sink,
        highlights: highlightsOf(feed.status?.detail, sink, feed),
        detailEntries: Object.entries(feed.status?.detail ?? {})
          .filter(([key]) => key !== 'leaseToken' && key !== 'emplacements' && key !== 'heartbeat')
          .map(([key, value]) => ({ key, value: formatDetail(value), withheld: isWithheld(value) })),
      };
    });
  });

  readonly listeningCount = computed(() => this.rows().filter((row) => row.listening).length);
  readonly visibleRows = computed(() => (this.filter() === 'listening' ? this.rows().filter((row) => row.listening) : this.rows()));

  private release: (() => void) | null = null;
  private clock: ReturnType<typeof setInterval> | null = null;
  private emplacementsLoad: Subscription | null = null;

  constructor() {
    // The expanded rows live in ?open=, so a link can open one (the entity
    // panel's "connection" link) and a reload keeps them.
    this.route.queryParamMap.pipe(takeUntilDestroyed()).subscribe((params) => {
      const open = params.get('open');
      this.expanded.set(new Set(open ? open.split(',').filter((id) => id !== '') : []));
    });
  }

  ngOnInit(): void {
    this.release = this.feedService.poll(10_000);
    this.reloadEmplacements();
    this.feedService.loadDrivers().subscribe({
      next: () => this.driversError.set(null),
      error: (err) => this.driversError.set(`Driver catalog unavailable: ${problemsOf(err).message}`),
    });
    // The emplacements are reloaded with the feed poll: the server moves them
    // (a deleted connection's operator rows go to Unassigned; a sender
    // declares new ones) without telling this page.
    this.clock = setInterval(() => {
      this.now.set(Date.now());
      this.reloadEmplacements();
    }, 10_000);
  }

  ngOnDestroy(): void {
    this.release?.();
    this.release = null;
    if (this.clock) {
      clearInterval(this.clock);
      this.clock = null;
    }
    this.emplacementsLoad?.unsubscribe();
    this.emplacementsLoad = null;
  }

  /** Loads the emplacements again, dropping a load still in flight. */
  private reloadEmplacements(): void {
    this.emplacementsLoad?.unsubscribe();
    this.emplacementsLoad = this.emplacements.load().subscribe({ error: () => undefined });
  }

  // --- presentation -----------------------------------------------------

  /** "host:port/protocol" for a TCP or UDP connection, null for the other drivers. */
  endpointOf(feed: Feed): string | null {
    const target = sendTarget(feed);
    return target ? endpointLabel(target) : null;
  }

  stateLabel(state: FeedState): string {
    return STATE_LABELS[state] ?? state;
  }

  healthLabel(health: FeedHealthLevel): string {
    return HEALTH_LABELS[health] ?? health;
  }

  directionLabel(direction: FeedDirection): string {
    return DIRECTION_LABELS[direction] ?? direction;
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
    const next = new Set(this.expanded());
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    this.expanded.set(next);
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: { open: next.size > 0 ? [...next].join(',') : null },
      queryParamsHandling: 'merge',
      replaceUrl: true,
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
        // The server unbound its emplacements: its operator rows are now Unassigned.
        this.reloadEmplacements();
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

/**
 * What the Source column shows: the label the server gives, else for TCP and UDP the format they
 * read (and, for Sentinel's own, the source it assumes), which is all they have to say about it.
 */
function sourceLabelOf(feed: Feed): string {
  if (feed.source) {
    return feed.source;
  }
  if (feed.driver !== 'tcp' && feed.driver !== 'udp') {
    return '';
  }
  const config = feed.config ?? {};
  const format = typeof config['format'] === 'string' && config['format'] !== '' ? config['format'] : 'sentinel';
  // Only Sentinel's own format has a default source; the form drops it for the others.
  const source = config['defaultSource'];
  return format === 'sentinel' && typeof source === 'string' && source !== '' ? `${format} · ${source}` : format;
}

/** The driver detail values worth a glance in the row. */
function highlightsOf(detail: Record<string, unknown> | undefined, sink: SinkDetail, feed: Feed): string[] {
  if (!detail) {
    return [];
  }
  const out: string[] = [];
  const sites = sitesSummaryOf(detail);
  if (sites) {
    out.push(sites);
  }
  if (sink.remotePeer) {
    out.push(sink.remotePeer);
  }
  // TLS is configured, not verified: the driver reports what it was set up with.
  if (sink.tls === true) {
    out.push('tls');
  }
  if (detail['idle'] === true) {
    out.push('idle');
  }
  if (typeof detail['creditsRemaining'] === 'number') {
    out.push(`${detail['creditsRemaining']} credits`);
  }
  if (typeof detail['effectiveIntervalSec'] === 'number') {
    out.push(`every ${detail['effectiveIntervalSec']}s`);
  }
  if (feed.driver === 'udp' || detail['transport'] === 'udp') {
    // Datagrams have no connections to count (it is always 0); who has been sending is the news.
    const senders = detail['distinctSenders'];
    if (typeof senders === 'number') {
      out.push(`${senders} sender${senders === 1 ? '' : 's'}`);
    }
  } else if (typeof detail['connections'] === 'number') {
    out.push(`${detail['connections']} conn`);
  }
  return out;
}

/**
 * Whether a detail value is blank because the server withheld it (it blanks the texts of a connection above the caller's
 * clearance) or never had it: an empty text, or a map in which every value is one.
 */
function isWithheld(value: unknown): boolean {
  if (value === '') {
    return true;
  }
  if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
    const values = Object.values(value);
    return values.length > 0 && values.every((v) => v === '');
  }
  return false;
}

function formatDetail(value: unknown): string {
  if (value === null || value === undefined || isWithheld(value)) {
    return '—';
  }
  if (typeof value === 'object') {
    return JSON.stringify(value);
  }
  return String(value);
}
