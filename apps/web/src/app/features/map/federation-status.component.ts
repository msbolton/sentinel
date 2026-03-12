import { Component, ChangeDetectionStrategy } from '@angular/core';
import { FederationService } from '../../core/services/federation.service';

@Component({
  selector: 'app-federation-status',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (federationService.federationActive()) {
      <div class="federation-badge">
        <div class="badge-header">FEDERATION</div>
        <div class="peer-list">
          @for (peer of federationService.peers(); track peer.instanceId) {
            <div class="peer-row">
              <span class="status-dot" [class]="'status-' + peer.status"></span>
              <span class="peer-name">{{ peer.displayName }}</span>
              <span class="peer-stats">{{ peer.userCount }}u / {{ peer.entityCount }}e</span>
            </div>
          }
        </div>
      </div>

      <div class="source-legend">
        <div class="legend-header">SOURCES</div>
        <div class="legend-items">
          <div class="legend-item">
            <span class="legend-dot" style="background: #3b82f6"></span>
            <span class="legend-name">Local</span>
          </div>
          @for (peer of federationService.peers(); track peer.instanceId) {
            <div class="legend-item">
              <span class="legend-dot" [style.background]="peer.color"></span>
              <span class="legend-name">{{ peer.displayName }}</span>
            </div>
          }
        </div>
      </div>
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
      padding: 8px 12px;
      font-size: 11px;
      pointer-events: auto;
      z-index: 10;
    }

    .badge-header {
      color: #888;
      font-weight: bold;
      margin-bottom: 4px;
      text-transform: uppercase;
      letter-spacing: 1px;
      font-size: 10px;
    }

    .peer-list {
      display: flex;
      flex-direction: column;
      gap: 3px;
    }

    .peer-row {
      display: flex;
      align-items: center;
      gap: 6px;
    }

    .status-dot {
      width: 6px;
      height: 6px;
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

    .peer-name {
      color: #ccc;
    }

    .peer-stats {
      color: #888;
      font-size: 10px;
      margin-left: auto;
    }

    .source-legend {
      position: absolute;
      bottom: 12px;
      left: 12px;
      background: rgba(0, 0, 0, 0.85);
      border: 1px solid #333;
      border-radius: 8px;
      padding: 10px 14px;
      font-size: 11px;
      pointer-events: auto;
      z-index: 10;
    }

    .legend-header {
      color: #888;
      font-weight: bold;
      margin-bottom: 6px;
      text-transform: uppercase;
      letter-spacing: 1px;
      font-size: 10px;
    }

    .legend-items {
      display: flex;
      gap: 12px;
      align-items: center;
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
    }
  `],
})
export class FederationStatusComponent {
  constructor(readonly federationService: FederationService) {}
}
