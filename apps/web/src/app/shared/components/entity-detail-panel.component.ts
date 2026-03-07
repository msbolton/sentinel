import { Component, input, output } from '@angular/core';
import { CommonModule } from '@angular/common';
import { Entity } from '../models/entity.model';

@Component({
  selector: 'app-entity-detail-panel',
  standalone: true,
  imports: [CommonModule],
  template: `
    @if (entity(); as e) {
      <aside class="detail-panel">
        <!-- Header -->
        <div class="panel-header">
          <div class="header-info">
            <h3 class="entity-name">{{ e.name }}</h3>
            <span class="chip chip-entity-type" [ngClass]="e.entityType">
              {{ e.entityType }}
            </span>
          </div>
          <div class="header-actions">
            <button class="btn btn-ghost btn-sm" (click)="flyTo.emit(e)" title="Fly To">
              &#9992;
            </button>
            <button class="btn btn-ghost btn-sm" (click)="close.emit()" title="Close">
              &times;
            </button>
          </div>
        </div>

        <div class="panel-body">
          <!-- Classification -->
          <section class="section">
            <h4 class="section-label">Classification</h4>
            <span class="chip chip-classification" [ngClass]="e.classification">
              {{ e.classification }}
            </span>
          </section>

          <!-- Position -->
          @if (e.position) {
            <section class="section">
              <h4 class="section-label">Position</h4>
              <div class="field-row">
                <span class="field-label">Lat</span>
                <span class="field-value mono">{{ e.position.latitude | number:'1.6-6' }}&deg;</span>
              </div>
              <div class="field-row">
                <span class="field-label">Lon</span>
                <span class="field-value mono">{{ e.position.longitude | number:'1.6-6' }}&deg;</span>
              </div>
              @if (e.position.altitude != null) {
                <div class="field-row">
                  <span class="field-label">Alt</span>
                  <span class="field-value mono">{{ e.position.altitude | number:'1.0-0' }} m</span>
                </div>
              }
            </section>
          }

          <!-- Movement -->
          @if (e.speedKnots != null || e.heading != null || e.course != null) {
            <section class="section">
              <h4 class="section-label">Movement</h4>
              @if (e.speedKnots != null) {
                <div class="field-row">
                  <span class="field-label">Speed</span>
                  <span class="field-value mono">{{ e.speedKnots | number:'1.1-1' }} kts</span>
                </div>
              }
              @if (e.heading != null) {
                <div class="field-row">
                  <span class="field-label">Heading</span>
                  <span class="field-value mono">{{ e.heading | number:'1.0-0' }}&deg;</span>
                </div>
              }
              @if (e.course != null) {
                <div class="field-row">
                  <span class="field-label">Course</span>
                  <span class="field-value mono">{{ e.course | number:'1.0-0' }}&deg;</span>
                </div>
              }
            </section>
          }

          <!-- Source & Timing -->
          <section class="section">
            <h4 class="section-label">Source & Timing</h4>
            <div class="field-row">
              <span class="field-label">Source</span>
              <span class="chip chip-source">{{ e.source }}</span>
            </div>
            <div class="field-row">
              <span class="field-label">Created</span>
              <span class="field-value mono">{{ e.createdAt | date:'short' }}</span>
            </div>
            <div class="field-row">
              <span class="field-label">Updated</span>
              <span class="field-value mono">{{ e.updatedAt | date:'short' }}</span>
            </div>
            @if (e.lastSeenAt) {
              <div class="field-row">
                <span class="field-label">Last Seen</span>
                <span class="field-value mono">{{ e.lastSeenAt | date:'short' }}</span>
              </div>
            }
          </section>

          <!-- Affiliations -->
          @if (e.affiliations.length > 0) {
            <section class="section">
              <h4 class="section-label">Affiliations</h4>
              <div class="chip-list">
                @for (aff of e.affiliations; track aff) {
                  <span class="chip chip-affiliation">{{ aff }}</span>
                }
              </div>
            </section>
          }

          <!-- Description -->
          @if (e.description) {
            <section class="section">
              <h4 class="section-label">Description</h4>
              <p class="description-text">{{ e.description }}</p>
            </section>
          }

          <!-- Metadata -->
          @if (e.metadata | keyvalue; as kvs) {
            @if (kvs.length > 0) {
              <section class="section">
                <h4 class="section-label">Metadata</h4>
                @for (kv of kvs; track kv.key) {
                  <div class="field-row">
                    <span class="field-label">{{ kv.key }}</span>
                    <span class="field-value mono">{{ kv.value }}</span>
                  </div>
                }
              </section>
            }
          }
        </div>
      </aside>
    }
  `,
  styles: [`
    .detail-panel {
      position: absolute;
      top: 12px;
      right: 12px;
      bottom: calc(var(--status-bar-height, 28px) + 12px);
      width: var(--panel-width, 380px);
      z-index: 100;
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      border-radius: var(--radius-lg);
      box-shadow: var(--shadow-lg);
      display: flex;
      flex-direction: column;
      animation: slideInRight 250ms ease;
      overflow: hidden;
    }

    @keyframes slideInRight {
      from { transform: translateX(100%); opacity: 0; }
      to   { transform: translateX(0);    opacity: 1; }
    }

    .panel-header {
      display: flex;
      align-items: flex-start;
      justify-content: space-between;
      padding: 16px;
      border-bottom: 1px solid var(--border-color);
      gap: 12px;
    }

    .header-info {
      display: flex;
      flex-direction: column;
      gap: 6px;
      min-width: 0;
    }

    .entity-name {
      font-size: 1rem;
      font-weight: 600;
      color: var(--text-primary);
      margin: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .header-actions {
      display: flex;
      gap: 4px;
      flex-shrink: 0;
    }

    .panel-body {
      flex: 1;
      overflow-y: auto;
      padding: 8px 16px 16px;
    }

    .section {
      padding: 12px 0;
      border-bottom: 1px solid var(--border-color);

      &:last-child {
        border-bottom: none;
      }
    }

    .section-label {
      font-size: 0.7rem;
      font-weight: 600;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.06em;
      margin: 0 0 8px;
    }

    .field-row {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 4px 0;
      gap: 12px;
    }

    .field-label {
      font-size: 0.8rem;
      color: var(--text-muted);
      flex-shrink: 0;
    }

    .field-value {
      font-size: 0.8rem;
      color: var(--text-secondary);
      text-align: right;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;

      &.mono {
        font-family: var(--font-mono);
      }
    }

    .chip-list {
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
    }

    .chip-source {
      background: color-mix(in srgb, var(--accent-cyan) 15%, transparent);
      color: var(--accent-cyan);
      border-color: color-mix(in srgb, var(--accent-cyan) 30%, transparent);
    }

    .chip-affiliation {
      background: color-mix(in srgb, var(--accent-purple) 15%, transparent);
      color: var(--accent-purple);
      border-color: color-mix(in srgb, var(--accent-purple) 30%, transparent);
    }

    .description-text {
      font-size: 0.8rem;
      color: var(--text-secondary);
      line-height: 1.5;
    }
  `],
})
export class EntityDetailPanelComponent {
  entity = input<Entity | null>(null);
  close = output<void>();
  flyTo = output<Entity>();
}
