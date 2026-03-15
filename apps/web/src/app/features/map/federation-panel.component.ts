import { Component, ChangeDetectionStrategy, signal, output } from '@angular/core';
import { FederationService } from '../../core/services/federation.service';

@Component({
  selector: 'app-federation-panel',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (federationService.federationActive()) {
      @if (!panelOpen()) {
        <div class="federation-badge" (click)="panelOpen.set(true)">
          <span class="badge-label">FED</span>
          <div class="badge-dots">
            @for (peer of federationService.peers(); track peer.instanceId) {
              <div [class]="'badge-dot status-' + peer.status"></div>
            }
          </div>
          <span class="badge-arrow">&#9662;</span>
        </div>
      }

      @if (panelOpen()) {
        <div class="federation-panel">
          <div class="panel-header">
            <span class="panel-title">Federation</span>
            <button class="panel-close" (click)="panelOpen.set(false)">&times;</button>
          </div>

          <div class="panel-body">
            <div class="panel-section">
              <div class="section-header">CONNECTED PEERS</div>
              @for (peer of federationService.peers(); track peer.instanceId) {
                <div class="peer-row" [class.peer-inactive]="peer.status !== 'connected'">
                  <div [class]="'status-dot status-' + peer.status"></div>
                  <div class="peer-info">
                    <div class="peer-name">{{ peer.displayName }}</div>
                    <div class="peer-meta">{{ peer.status === 'connected' ? peer.entityCount + ' entities' : peer.status === 'stale' ? 'Stale' : 'Disconnected' }}</div>
                  </div>
                  <div class="peer-color" [style.background]="peer.color"></div>
                </div>
              }
            </div>

            <div class="panel-divider"></div>

            <div class="panel-section visibility-section">
              <div class="section-header">VISIBILITY</div>
              @for (peer of federationService.peers(); track peer.instanceId) {
                <label class="visibility-row">
                  <input
                    type="checkbox"
                    checked
                    [style.accent-color]="peer.color"
                    (change)="onToggle(peer.instanceId, $event)"
                  />
                  <span class="peer-color" [style.background]="peer.color"></span>
                  <span class="visibility-name">{{ peer.displayName }}</span>
                </label>
              }
            </div>

            <div class="panel-divider"></div>

            <div class="panel-section">
              <div class="section-header">SOURCE LEGEND</div>
              <div class="legend-items">
                <div class="legend-item">
                  <span class="legend-dot" style="background: #3b82f6"></span>
                  <span class="legend-name">Local</span>
                </div>
                @for (peer of federationService.peers(); track peer.instanceId) {
                  @if (peer.status === 'connected') {
                    <div class="legend-item">
                      <span class="legend-dot" [style.background]="peer.color"></span>
                      <span class="legend-name">{{ peer.displayName }}</span>
                    </div>
                  }
                }
              </div>
            </div>
          </div>
        </div>
      }
    }
  `,
  styles: [`
    :host {
      pointer-events: none;
    }

    .federation-badge {
      position: absolute;
      top: 12px;
      right: 12px;
      background: rgba(0, 0, 0, 0.85);
      border: 1px solid #333;
      border-radius: 8px;
      padding: 6px 10px;
      display: flex;
      align-items: center;
      gap: 6px;
      cursor: pointer;
      pointer-events: auto;
      z-index: 10;
      transition: background 0.15s;
    }
    .federation-badge:hover {
      background: rgba(30, 40, 60, 0.9);
      border-color: rgba(255, 255, 255, 0.2);
    }

    .badge-label {
      color: #888;
      font-weight: bold;
      letter-spacing: 1px;
      font-size: 9px;
    }

    .badge-dots {
      display: flex;
      gap: 3px;
    }

    .badge-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
    }

    .badge-arrow {
      color: #666;
      font-size: 10px;
    }

    .federation-panel {
      position: absolute;
      top: 12px;
      right: 12px;
      width: 260px;
      background: rgba(10, 14, 23, 0.95);
      border: 1px solid #333;
      border-radius: 8px;
      overflow: hidden;
      pointer-events: auto;
      z-index: 10;
      backdrop-filter: blur(16px);
      box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
    }

    .panel-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 10px 14px;
      border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    }
    .panel-title {
      color: #ccc;
      font-weight: 600;
      font-size: 12px;
    }
    .panel-close {
      background: none;
      border: none;
      color: #666;
      font-size: 16px;
      cursor: pointer;
      padding: 0 2px;
      line-height: 1;
    }
    .panel-close:hover { color: #ccc; }

    .panel-body {
      padding: 0;
    }

    .panel-section {
      padding: 10px 14px;
    }

    .panel-divider {
      border-top: 1px solid rgba(255, 255, 255, 0.06);
      margin: 0 14px;
    }

    .section-header {
      color: #888;
      font-size: 9px;
      text-transform: uppercase;
      letter-spacing: 1px;
      margin-bottom: 8px;
    }

    .peer-row {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 7px 10px;
      background: rgba(255, 255, 255, 0.03);
      border-radius: 6px;
      margin-bottom: 4px;
    }
    .peer-inactive { opacity: 0.6; }

    .status-dot {
      width: 7px;
      height: 7px;
      border-radius: 50%;
      flex-shrink: 0;
    }
    .status-connected {
      background: #22c55e;
      box-shadow: 0 0 4px #22c55e;
    }
    .status-stale {
      background: #eab308;
      box-shadow: 0 0 4px #eab308;
    }
    .status-disconnected {
      background: #ef4444;
      box-shadow: 0 0 4px #ef4444;
    }

    .peer-info { flex: 1; }
    .peer-name {
      color: #ccc;
      font-size: 11px;
      font-weight: 500;
    }
    .peer-meta {
      color: #666;
      font-size: 9px;
    }

    .peer-color {
      width: 10px;
      height: 10px;
      border-radius: 2px;
      flex-shrink: 0;
    }

    .visibility-row {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 4px 0;
      cursor: pointer;
    }
    .visibility-row input[type="checkbox"] {
      width: 13px;
      height: 13px;
      cursor: pointer;
    }
    .visibility-name {
      color: #ccc;
      font-size: 11px;
    }

    .legend-items {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
    }
    .legend-item {
      display: flex;
      align-items: center;
      gap: 5px;
    }
    .legend-dot {
      width: 8px;
      height: 8px;
      border-radius: 2px;
      flex-shrink: 0;
    }
    .legend-name {
      color: #ccc;
      font-size: 10px;
    }
  `],
})
export class FederationPanelComponent {
  readonly panelOpen = signal(false);
  readonly togglePeer = output<{ instanceId: string; visible: boolean }>();

  constructor(readonly federationService: FederationService) {}

  onToggle(instanceId: string, event: Event): void {
    const visible = (event.target as HTMLInputElement).checked;
    this.togglePeer.emit({ instanceId, visible });
  }
}
