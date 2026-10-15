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
import { FormsModule } from '@angular/forms';
import { DataFeedService, DataFeed, CreateFeedRequest } from '../../core/services/data-feed.service';

interface DataLayerConfig {
  id: string;
  name: string;
  source: string;
}

interface DataLayer {
  id: string;
  name: string;
  source: string;
  count: number | null;
  enabled: boolean;
  custom: boolean;
  lastUpdated: string;
  freshness: 'green' | 'yellow' | 'red' | 'gray' | 'none';
  errorCount: number;
}

@Component({
  selector: 'app-data-feeds',
  standalone: true,
  imports: [FormsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="pill-container" [class.expanded]="expanded()">
      <!-- Pill / Header bar -->
      <button class="pill-header" (click)="toggle()">
        <span class="pill-label">DATA LAYERS</span>
        @if (!expanded() && activeCount() > 0) {
          <span class="feed-badge">{{ activeCount() }}</span>
        }
        <span class="pill-rule" [class.visible]="expanded()"></span>
        <span class="pill-toggle-btn">{{ expanded() ? '−' : '+' }}</span>
      </button>

      <!-- Expanded panel -->
      @if (expanded()) {
        <div class="pill-panel">
          <!-- Inline Add Feed Form -->
          @if (showAddForm()) {
            <div class="feed-form">
              <div class="form-group">
                <label>Name</label>
                <input type="text" [(ngModel)]="formName" placeholder="My MQTT Feed" />
              </div>
              <div class="form-group">
                <label>Connector</label>
                <select [(ngModel)]="formConnectorType" (ngModelChange)="onConnectorChange()">
                  <option value="mqtt">MQTT</option>
                  <option value="stomp">STOMP</option>
                  <option value="tcp">TCP</option>
                </select>
              </div>
              <div class="form-group">
                <label>Format</label>
                <select [(ngModel)]="formFormat">
                  <option value="json">JSON</option>
                  <option value="nmea">NMEA</option>
                  <option value="cot">CoT</option>
                  <option value="ais">AIS</option>
                  <option value="adsb">ADS-B</option>
                  <option value="link16">Link 16</option>
                </select>
              </div>

              <!-- MQTT config -->
              @if (formConnectorType === 'mqtt') {
                <div class="form-group">
                  <label>Broker URL</label>
                  <input type="text" [(ngModel)]="formBrokerUrl" placeholder="tcp://broker:1883" />
                </div>
                <div class="form-group">
                  <label>Topics</label>
                  <input type="text" [(ngModel)]="formTopics" placeholder="sensors/#, track/#" />
                </div>
                <div class="form-group">
                  <label>QoS</label>
                  <select [(ngModel)]="formQos">
                    <option [ngValue]="0">0 (At most once)</option>
                    <option [ngValue]="1">1 (At least once)</option>
                    <option [ngValue]="2">2 (Exactly once)</option>
                  </select>
                </div>
              }

              <!-- STOMP config -->
              @if (formConnectorType === 'stomp') {
                <div class="form-group">
                  <label>Broker URL</label>
                  <input type="text" [(ngModel)]="formBrokerUrl" placeholder="broker:61613" />
                </div>
                <div class="form-group">
                  <label>Queue</label>
                  <input type="text" [(ngModel)]="formQueue" placeholder="/queue/sensor-feeds" />
                </div>
              }

              <!-- TCP config -->
              @if (formConnectorType === 'tcp') {
                <div class="form-group">
                  <label>Address</label>
                  <input type="text" [(ngModel)]="formAddress" placeholder="0.0.0.0:5001" />
                </div>
              }

              <div class="form-actions">
                <button class="form-btn cancel" (click)="cancelAddFeed()">Cancel</button>
                <button
                  class="form-btn create"
                  [disabled]="!isAddFormValid() || saving()"
                  (click)="submitAddFeed()">
                  {{ saving() ? 'Creating...' : 'Create' }}
                </button>
              </div>
            </div>
          }

          @for (layer of layers(); track layer.id) {
            <div class="layer-row">
              <div class="layer-content">
                <div class="layer-main">
                  <span class="freshness-dot" [attr.data-status]="layer.freshness"></span>
                  <span class="layer-name">{{ layer.name }}</span>
                  <span class="layer-count">{{ layer.count !== null ? formatCount(layer.count) : '—' }}</span>
                  <button
                    class="layer-toggle"
                    [class.on]="layer.enabled"
                    [class.busy]="toggling() === layer.id"
                    [disabled]="toggling() === layer.id"
                    (click)="toggleLayer(layer)">
                    {{ layer.enabled ? 'ON' : 'OFF' }}
                  </button>
                  @if (layer.custom) {
                    <button class="layer-delete" (click)="deleteFeed(layer.id); $event.stopPropagation()">&times;</button>
                  }
                </div>
                <div class="layer-meta">
                  {{ layer.source }} · {{ layer.lastUpdated }}
                  @if (layer.errorCount > 0) {
                    · <span class="error-count">{{ layer.errorCount }} errors</span>
                  }
                </div>
              </div>
            </div>
          }
          @if (layers().length === 0) {
            <div class="layer-empty">
              @if (feedService.hasLoaded) {
                No layers configured
              } @else {
                Ingest service offline
              }
            </div>
          }
          <button class="add-feed-btn bottom" (click)="startAddFeed(); $event.stopPropagation()">+ ADD FEED</button>
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

    .add-feed-btn {
      display: block;
      width: 100%;
      padding: 8px 10px;
      margin-top: 4px;
      border-radius: var(--radius-sm);
      font-family: var(--font-mono);
      font-size: 0.7rem;
      font-weight: 600;
      letter-spacing: 0.08em;
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

    .feed-form {
      padding: 10px;
      margin-bottom: 6px;
      border: 1px solid var(--border-color);
      border-radius: var(--radius-sm);
      background: rgba(255, 255, 255, 0.02);
    }

    .form-group {
      display: flex;
      flex-direction: column;
      gap: 3px;
      margin-bottom: 8px;

      label {
        font-family: var(--font-mono);
        font-size: 0.6rem;
        text-transform: uppercase;
        letter-spacing: 0.1em;
        color: var(--text-muted);
      }

      input, select {
        padding: 5px 8px;
        border-radius: 4px;
        border: 1px solid var(--border-color);
        background: rgba(0, 0, 0, 0.2);
        color: var(--text-primary);
        font-family: var(--font-mono);
        font-size: 0.75rem;

        &:focus {
          outline: none;
          border-color: var(--accent-cyan, var(--accent-blue));
        }
      }
    }

    .form-actions {
      display: flex;
      gap: 8px;
      justify-content: flex-end;
      margin-top: 10px;
    }

    .form-btn {
      padding: 5px 14px;
      border-radius: 4px;
      font-family: var(--font-mono);
      font-size: 0.7rem;
      font-weight: 600;
      letter-spacing: 0.06em;
      cursor: pointer;
      border: 1px solid var(--border-color);
      transition: all 150ms ease;

      &.cancel {
        background: transparent;
        color: var(--text-muted);

        &:hover {
          color: var(--text-primary);
        }
      }

      &.create {
        background: rgba(6, 182, 212, 0.15);
        color: var(--accent-cyan, var(--accent-blue));
        border-color: var(--accent-cyan, var(--accent-blue));

        &:hover:not(:disabled) {
          background: rgba(6, 182, 212, 0.3);
        }

        &:disabled {
          opacity: 0.4;
          cursor: not-allowed;
        }
      }
    }

    .layer-row {
      display: flex;
      align-items: center;
      padding: 8px 10px;
      border-radius: var(--radius-sm);
      transition: background var(--transition-fast);

      &:hover {
        background: color-mix(in srgb, var(--text-muted) 8%, transparent);
      }
    }

    .layer-content {
      display: flex;
      flex-direction: column;
      gap: 3px;
      min-width: 0;
      width: 100%;
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

    .layer-toggle {
      padding: 4px 12px;
      border-radius: 4px;
      font-family: var(--font-mono);
      font-size: 0.7rem;
      font-weight: 600;
      letter-spacing: 0.1em;
      background: rgba(255, 255, 255, 0.05);
      color: var(--text-muted);
      border: 1px solid var(--border-color);
      cursor: pointer;
      flex-shrink: 0;
      transition: all 150ms ease;

      &.on {
        color: var(--accent-cyan, var(--accent-blue));
        border-color: var(--accent-cyan, var(--accent-blue));
        background: rgba(6, 182, 212, 0.1);
      }

      &.busy {
        opacity: 0.5;
        cursor: wait;
      }

      &:disabled {
        pointer-events: none;
      }
    }

    .layer-delete {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 22px;
      height: 22px;
      border-radius: 4px;
      background: transparent;
      border: 1px solid transparent;
      color: var(--text-muted);
      font-size: 1rem;
      cursor: pointer;
      flex-shrink: 0;
      transition: all 150ms ease;

      &:hover {
        color: #ef4444;
        border-color: #ef4444;
        background: rgba(239, 68, 68, 0.1);
      }
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

    .error-count {
      color: #ef4444;
    }

    .layer-empty {
      padding: 12px 10px;
      font-size: 0.75rem;
      color: var(--text-muted);
      text-align: center;
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
export class DataFeedsComponent implements OnInit, OnDestroy {
  readonly feedService = inject(DataFeedService);
  private readonly elementRef = inject(ElementRef);

  readonly expanded = signal(false);
  readonly toggling = signal<string | null>(null);
  readonly showAddForm = signal(false);
  readonly saving = signal(false);
  private readonly localOverrides = signal<Map<string, boolean>>(new Map());
  private refreshInterval?: ReturnType<typeof setInterval>;

  // Form fields
  formName = '';
  formConnectorType: 'mqtt' | 'stomp' | 'tcp' = 'mqtt';
  formFormat: 'json' | 'nmea' | 'cot' | 'ais' | 'adsb' | 'link16' = 'json';
  formBrokerUrl = '';
  formTopics = '';
  formQos = 1;
  formQueue = '';
  formAddress = '';

  private readonly LAYER_CONFIG: DataLayerConfig[] = [
    { id: 'opensky',     name: 'Live Flights',      source: 'OpenSky Network' },
    { id: 'adsb-lol',    name: 'Military Flights',   source: 'adsb.lol' },
    { id: 'celestrak',   name: 'Satellites',          source: 'CelesTrak' },
    { id: 'nexrad',      name: 'Weather Radar',       source: 'NOAA NEXRAD (globe overlay)' },
    { id: 'cctv',        name: 'CCTV Mesh',           source: 'CCTV Mesh + Street View fallback' },
  ];

  readonly layers = computed<DataLayer[]>(() => {
    const feeds = this.feedService.feeds();
    const overrides = this.localOverrides();
    const feedMap = new Map(feeds.map((f) => [f.id, f]));

    // Start with configured layers, merging live feed data
    const layers: DataLayer[] = this.LAYER_CONFIG.map((config) => {
      const feed = feedMap.get(config.id);
      const baseEnabled = feed?.enabled ?? false;
      return {
        id: config.id,
        name: config.name,
        source: config.source,
        count: feed ? this.extractCount(feed) : null,
        enabled: overrides.has(config.id) ? overrides.get(config.id)! : baseEnabled,
        custom: false,
        lastUpdated: feed ? this.getRelativeTime(feed) : 'never',
        freshness: this.healthToFreshness(feed),
        errorCount: feed?.health?.errorCount ?? 0,
      };
    });

    // Append any feeds not in the static config (including custom feeds)
    const configIds = new Set(this.LAYER_CONFIG.map((c) => c.id));
    for (const feed of feeds) {
      if (!configIds.has(feed.id)) {
        const baseEnabled = feed.enabled;
        layers.push({
          id: feed.id,
          name: feed.name,
          source: feed.sourceType,
          count: this.extractCount(feed),
          enabled: overrides.has(feed.id) ? overrides.get(feed.id)! : baseEnabled,
          custom: feed.custom ?? false,
          lastUpdated: this.getRelativeTime(feed),
          freshness: this.healthToFreshness(feed),
          errorCount: feed.health?.errorCount ?? 0,
        });
      }
    }

    return layers;
  });

  readonly activeCount = computed(() =>
    this.layers().filter((l) => l.enabled).length,
  );

  ngOnInit(): void {
    this.feedService.loadFeeds();

    this.refreshInterval = setInterval(() => {
      if (this.expanded()) {
        this.feedService.loadFeeds();
      }
    }, 30_000);
  }

  ngOnDestroy(): void {
    if (this.refreshInterval) {
      clearInterval(this.refreshInterval);
    }
  }

  toggle(): void {
    this.expanded.update((v) => !v);
    if (this.expanded()) {
      this.feedService.loadFeeds();
    }
  }

  toggleLayer(layer: DataLayer): void {
    const newEnabled = !layer.enabled;

    this.localOverrides.update((m) => {
      const next = new Map(m);
      next.set(layer.id, newEnabled);
      return next;
    });

    this.toggling.set(layer.id);
    this.feedService.toggleFeed(layer.id, newEnabled).subscribe({
      next: () => {
        this.localOverrides.update((m) => {
          const next = new Map(m);
          next.delete(layer.id);
          return next;
        });
        this.toggling.set(null);
      },
      error: (err) => {
        console.error('Failed to toggle layer:', err);
        this.localOverrides.update((m) => {
          const next = new Map(m);
          next.delete(layer.id);
          return next;
        });
        this.toggling.set(null);
      },
    });
  }

  startAddFeed(): void {
    this.resetForm();
    this.showAddForm.set(true);
  }

  cancelAddFeed(): void {
    this.showAddForm.set(false);
  }

  onConnectorChange(): void {
    this.formBrokerUrl = '';
    this.formTopics = '';
    this.formQos = 1;
    this.formQueue = '';
    this.formAddress = '';
  }

  isAddFormValid(): boolean {
    if (!this.formName.trim()) return false;
    switch (this.formConnectorType) {
      case 'mqtt':
        return !!this.formBrokerUrl.trim() && !!this.formTopics.trim();
      case 'stomp':
        return !!this.formBrokerUrl.trim() && !!this.formQueue.trim();
      case 'tcp':
        return !!this.formAddress.trim();
      default:
        return false;
    }
  }

  submitAddFeed(): void {
    if (!this.isAddFormValid()) return;

    let config: Record<string, unknown>;
    switch (this.formConnectorType) {
      case 'mqtt':
        config = {
          broker_url: this.formBrokerUrl.trim(),
          topics: this.formTopics.split(',').map(t => t.trim()).filter(Boolean),
          qos: this.formQos,
        };
        break;
      case 'stomp':
        config = {
          broker_url: this.formBrokerUrl.trim(),
          queue: this.formQueue.trim(),
        };
        break;
      case 'tcp':
        config = {
          address: this.formAddress.trim(),
        };
        break;
    }

    const request: CreateFeedRequest = {
      name: this.formName.trim(),
      connector_type: this.formConnectorType,
      format: this.formFormat,
      config,
    };

    this.saving.set(true);
    this.feedService.createFeed(request).subscribe({
      next: () => {
        this.saving.set(false);
        this.showAddForm.set(false);
      },
      error: (err) => {
        console.error('Failed to create feed:', err);
        this.saving.set(false);
      },
    });
  }

  deleteFeed(id: string): void {
    this.feedService.deleteFeed(id).subscribe({
      error: (err) => console.error('Failed to delete feed:', err),
    });
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
    if (!this.elementRef.nativeElement.contains(event.target)) {
      this.expanded.set(false);
    }
  }

  private resetForm(): void {
    this.formName = '';
    this.formConnectorType = 'mqtt';
    this.formFormat = 'json';
    this.formBrokerUrl = '';
    this.formTopics = '';
    this.formQos = 1;
    this.formQueue = '';
    this.formAddress = '';
  }

  private healthToFreshness(feed?: DataFeed): 'green' | 'yellow' | 'red' | 'gray' | 'none' {
    if (!feed?.health) return 'none';
    switch (feed.health.status) {
      case 'healthy': return 'green';
      case 'warn': return 'yellow';
      case 'critical': return 'red';
      case 'unknown': return 'gray';
      default: return 'none';
    }
  }

  private extractCount(feed: DataFeed): number | null {
    return feed.health?.entitiesCount ?? null;
  }

  private getRelativeTime(feed: DataFeed): string {
    if (!feed.health?.lastSuccessAt) {
      return feed.enabled ? 'waiting...' : 'never';
    }
    const lastSeen = new Date(feed.health.lastSuccessAt);
    const ageSec = Math.floor((Date.now() - lastSeen.getTime()) / 1000);
    if (ageSec < 5) return 'just now';
    if (ageSec < 60) return `${ageSec}s ago`;
    if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`;
    return `${Math.floor(ageSec / 3600)}h ago`;
  }
}
