import { ChangeDetectionStrategy, Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { map } from 'rxjs';
import { AlertService } from '../../core/services/alert.service';
import { AuthService } from '../../core/services/auth.service';
import { EntityService } from '../../core/services/entity.service';
import { FeedService } from '../../core/services/feed.service';
import { MapCursorService } from '../../core/services/map-cursor.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { SelectionService } from '../../core/services/selection.service';
import { ConnectionStatus, WebSocketService } from '../../core/services/websocket.service';
import { DemoBadgeComponent } from '../../features/demo/demo-badge.component';
import { formatDtg } from '../dtg';
import { formatAltitude, formatCoordinate, formatMgrs } from '../geo-format';
import { bearingDeg, rangeNm, viewWidthNm } from '../geo-math';
import { directionOf } from '../models/feed.model';
import { UserMenuComponent } from './user-menu.component';

/**
 * The glass-cockpit readouts in the four corners of the map, plus the
 * classification word at the top centre. Read-only: only the user menu
 * takes pointer events, so the map underneath keeps every click and drag.
 */
@Component({
  selector: 'app-hud-readouts',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [DemoBadgeComponent, UserMenuComponent],
  template: `
    <div class="hud-cls" data-readout="classification">{{ classification() }}</div>

    <div class="hud hud-tl">
      <div><span class="strong">SENTINEL</span> <span class="dim">·</span> {{ opName }}</div>
      @if (isGuest()) {
        <app-demo-badge [compact]="false" />
      }
      <div data-readout="dtg">{{ dtg() }}</div>
      <div data-readout="feeds"><span class="dim">feeds</span> {{ feedHealth().fresh }}/{{ feedHealth().total }}</div>
    </div>

    <div class="hud hud-tr">
      <div>
        <span class="dim">trk</span> <span class="strong" data-readout="tracks">{{ trackCount() }}</span>
        <span class="dim">alr</span> <span class="alr" [class.hot]="alertCount() > 0" data-readout="alerts">{{ alertCount() }}</span>
      </div>
      <app-user-menu class="hud" [expanded]="false" />
      <div data-readout="clearance"><span class="dim">clr</span> {{ classification() }}</div>
    </div>

    <div class="hud hud-bl">
      <div data-readout="cursor">{{ cursorText() }}</div>
      <div data-readout="mgrs"><span class="dim">MGRS</span> {{ mgrsText() }}</div>
      <div data-readout="view">
        <span class="dim">view</span> {{ viewText() }}
        @if (altitudeText(); as alt) {
          <span class="dim">alt</span> {{ alt }}
        }
      </div>
    </div>

    <div class="hud hud-br">
      <div data-readout="bearing"><span class="dim">cursor →</span> {{ bearingText() }}</div>
      <div data-readout="ws"><span class="dim">ws</span> {{ wsStatus() }}</div>
    </div>
  `,
  styles: [`
    :host {
      position: absolute;
      inset: 0;
      pointer-events: none;
      z-index: 60;
      font-family: var(--font-mono);
      font-size: 12px;
      line-height: 1.5;
      color: var(--text-primary);
    }

    .hud {
      position: absolute;
      padding: 10px 12px;
      white-space: nowrap;
    }

    .hud-tl { left: 0; top: 0; }
    .hud-tr { right: 0; top: 0; text-align: right; }
    .hud-bl { left: 0; bottom: 0; }
    .hud-br { right: 0; bottom: 0; text-align: right; }

    .dim { color: var(--text-muted); }
    .strong { color: var(--accent-cyan); }
    .alr.hot { color: var(--accent-yellow); }

    app-user-menu {
      pointer-events: auto;
    }

    .hud-tl app-demo-badge {
      pointer-events: auto;
    }

    /* A route panel covers the right edge of the map; the right-hand readouts move clear of it. */
    :host(.panel-open) .hud-tr,
    :host(.panel-open) .hud-br {
      right: var(--panel-width);
    }

    .hud-cls {
      position: absolute;
      top: 0;
      left: 50%;
      transform: translateX(-50%);
      padding: 2px 10px;
      font-size: 10px;
      letter-spacing: 0.2em;
      color: var(--accent-green);
    }
  `],
})
export class HudReadoutsComponent {
  private readonly auth = inject(AuthService);
  private readonly feeds = inject(FeedService);
  private readonly cursor = inject(MapCursorService);
  private readonly viewport = inject(MapViewportService);
  private readonly selection = inject(SelectionService);

  /** There is no operation concept yet; the map is the only one. */
  readonly opName = 'MAP';

  private readonly profile = toSignal(this.auth.userProfile$, { initialValue: null });
  readonly isGuest = computed(() => this.profile()?.roles?.includes('sentinel-guest') ?? false);
  readonly classification = computed(() => this.profile()?.classificationLevel ?? 'UNCLASSIFIED');

  readonly trackCount = toSignal(inject(EntityService).currentEntities$.pipe(map((m) => m.size)), { initialValue: 0 });
  readonly alertCount = toSignal(inject(AlertService).unacknowledgedCount$, { initialValue: 0 });
  readonly wsStatus = toSignal(inject(WebSocketService).connectionStatus$, {
    initialValue: 'disconnected' as ConnectionStatus,
  });

  private readonly now = signal(new Date());
  readonly dtg = computed(() => formatDtg(this.now()));

  /** Inbound, enabled connections, and of those the ones reporting healthy (the layers pill's green dot). */
  readonly feedHealth = computed(() => {
    const live = this.feeds.feeds().filter((f) => f.enabled && directionOf(f) !== 'out');
    return { total: live.length, fresh: live.filter((f) => f.status?.health === 'healthy').length };
  });

  readonly cursorText = computed(() => {
    const p = this.cursor.cursor();
    return p ? `${formatCoordinate(p.lat, 'lat')} ${formatCoordinate(p.lon, 'lon')}` : '—';
  });

  readonly mgrsText = computed(() => {
    const p = this.cursor.cursor();
    return p ? formatMgrs(p.lat, p.lon) : '—';
  });

  readonly viewText = computed(() => {
    const b = this.viewport.bounds();
    return b ? `${Math.round(viewWidthNm(b))} NM` : '—';
  });

  readonly altitudeText = computed(() => {
    const m = this.cursor.cameraAltitudeM();
    return m === null ? null : formatAltitude(m);
  });

  readonly bearingText = computed(() => {
    const p = this.cursor.cursor();
    const entity = this.selection.entity();
    const pos = entity?.position;
    if (!p || !entity || !pos) return '—';
    const target = { lat: pos.latitude, lon: pos.longitude };
    const brg = String(Math.round(bearingDeg(p, target)) % 360).padStart(3, '0');
    return `${entity.name || entity.id} ${brg}° / ${Math.round(rangeNm(p, target))} NM`;
  });

  constructor() {
    const timer = setInterval(() => this.now.set(new Date()), 1000);
    inject(DestroyRef).onDestroy(() => clearInterval(timer));
  }
}
