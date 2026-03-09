import { Component, computed, CUSTOM_ELEMENTS_SCHEMA, effect, inject, input, output, signal } from '@angular/core';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { CommonModule } from '@angular/common';
import { Entity } from '../models/entity.model';

@Component({
  selector: 'app-entity-detail-panel',
  standalone: true,
  imports: [CommonModule],
  schemas: [CUSTOM_ELEMENTS_SCHEMA],
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
            @if (e.countryOfOrigin) {
              <span class="chip chip-country">{{ e.countryOfOrigin }}</span>
            }
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

          <!-- Identity -->
          @if (primaryId() || e.affiliation) {
            <section class="section">
              <h4 class="section-label">Identity</h4>
              @if (primaryId(); as pid) {
                <div class="field-row">
                  <span class="field-label">{{ pid.label }}</span>
                  <span class="field-value mono">{{ pid.value }}</span>
                </div>
              }
              @if (e.sourceEntityId && e.sourceEntityId !== primaryId()?.value) {
                <div class="field-row">
                  <span class="field-label">Source ID</span>
                  <span class="field-value mono">{{ e.sourceEntityId }}</span>
                </div>
              }
              @for (sid of secondaryIds(); track sid.label) {
                <div class="field-row">
                  <span class="field-label">{{ sid.label }}</span>
                  <span class="field-value mono">{{ sid.value }}</span>
                </div>
              }
              @if (e.affiliation) {
                <div class="field-row">
                  <span class="field-label">Affiliation</span>
                  <span class="chip chip-affiliation-id" [ngClass]="affiliationColor()">
                    {{ e.affiliation }}
                  </span>
                </div>
              }
            </section>
          }

          <!-- 3D Model -->
          @if (modelSrc()) {
            <section class="section model-section">
              <iframe
                [srcdoc]="modelViewerHtml()"
                style="width:100%;height:200px;display:block;border:none;border-radius:8px;background:transparent"
              ></iframe>
            </section>
          }

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
              @if (e.circularError != null) {
                <div class="field-row">
                  <span class="field-label">CEP</span>
                  <span class="field-value mono">{{ e.circularError | number:'1.0-0' }} m</span>
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
              @if (e.kinematics?.velocity; as vel) {
                <div class="field-row">
                  <span class="field-label">Velocity</span>
                  <span class="field-value mono">
                    N{{ vel.north | number:'1.1-1' }} E{{ vel.east | number:'1.1-1' }} U{{ vel.up | number:'1.1-1' }} m/s
                  </span>
                </div>
              }
            </section>
          }

          <!-- Operational Status -->
          @if (e.operationalStatus && e.operationalStatus !== 'UNKNOWN') {
            <section class="section">
              <h4 class="section-label">Operational Status</h4>
              <div class="field-row">
                <span class="field-label">Status</span>
                <span class="chip chip-op-status" [ngClass]="e.operationalStatus">
                  {{ e.operationalStatus }}
                </span>
              </div>
              @if (e.damageAssessment && e.damageAssessment !== 'UNKNOWN' && e.damageAssessment !== 'NONE') {
                <div class="field-row">
                  <span class="field-label">Damage</span>
                  <span class="chip chip-damage" [ngClass]="e.damageAssessment">
                    {{ e.damageAssessment }}
                  </span>
                </div>
              }
              @if (e.dimensions) {
                <div class="field-row">
                  <span class="field-label">Dimensions</span>
                  <span class="field-value mono">
                    @if (e.dimensions.length != null) { {{ e.dimensions.length | number:'1.0-1' }}m }
                    @if (e.dimensions.width != null) { &times; {{ e.dimensions.width | number:'1.0-1' }}m }
                    @if (e.dimensions.height != null) { &times; {{ e.dimensions.height | number:'1.0-1' }}m }
                  </span>
                </div>
              }
            </section>
          }

          <!-- Platform Details (collapsible) -->
          @if (platformType()) {
            <section class="section">
              <div class="collapsible-header" (click)="toggleSection('platformDetails')">
                <h4 class="section-label" style="margin:0">
                  @switch (platformType()) {
                    @case ('ais') { Vessel Details }
                    @case ('adsb') { Aircraft Details }
                    @case ('tle') { Satellite Orbit }
                    @case ('cot') { CoT Data }
                    @case ('link16') { Link 16 Data }
                    @case ('uav') { UAV Data }
                  }
                </h4>
                <span class="chevron" [class.expanded]="expandedSections()['platformDetails']">&#9654;</span>
              </div>
              @if (expandedSections()['platformDetails']) {
                <div class="collapsible-content">
                  @switch (platformType()) {
                    @case ('ais') {
                      @if (e.platformData?.ais; as ais) {
                        @if (ais.shipTypeName) {
                          <div class="field-row"><span class="field-label">Ship Type</span><span class="field-value">{{ ais.shipTypeName }}</span></div>
                        }
                        @if (ais.navStatus && ais.navStatus !== 'UNKNOWN') {
                          <div class="field-row"><span class="field-label">Nav Status</span><span class="field-value">{{ ais.navStatus }}</span></div>
                        }
                        @if (ais.destination) {
                          <div class="field-row"><span class="field-label">Destination</span><span class="field-value">{{ ais.destination }}</span></div>
                        }
                        @if (ais.eta) {
                          <div class="field-row"><span class="field-label">ETA</span><span class="field-value mono">{{ ais.eta | date:'short' }}</span></div>
                        }
                        @if (ais.draught != null) {
                          <div class="field-row"><span class="field-label">Draught</span><span class="field-value mono">{{ ais.draught | number:'1.1-1' }} m</span></div>
                        }
                        @if (ais.lengthOverall != null) {
                          <div class="field-row"><span class="field-label">Length</span><span class="field-value mono">{{ ais.lengthOverall | number:'1.0-0' }} m</span></div>
                        }
                        @if (ais.beam != null) {
                          <div class="field-row"><span class="field-label">Beam</span><span class="field-value mono">{{ ais.beam | number:'1.0-0' }} m</span></div>
                        }
                        @if (ais.flag) {
                          <div class="field-row"><span class="field-label">Flag</span><span class="field-value">{{ ais.flag }}</span></div>
                        }
                        @if (ais.rateOfTurn != null) {
                          <div class="field-row"><span class="field-label">Rate of Turn</span><span class="field-value mono">{{ ais.rateOfTurn | number:'1.1-1' }}&deg;/min</span></div>
                        }
                        @if (ais.trueHeading != null && ais.trueHeading !== 511) {
                          <div class="field-row"><span class="field-label">True Heading</span><span class="field-value mono">{{ ais.trueHeading | number:'1.0-0' }}&deg;</span></div>
                        }
                        @if (ais.speedOverGround != null) {
                          <div class="field-row"><span class="field-label">SOG</span><span class="field-value mono">{{ ais.speedOverGround | number:'1.1-1' }} kts</span></div>
                        }
                        @if (ais.courseOverGround != null) {
                          <div class="field-row"><span class="field-label">COG</span><span class="field-value mono">{{ ais.courseOverGround | number:'1.1-1' }}&deg;</span></div>
                        }
                        @if (ais.positionAccuracyHigh != null) {
                          <div class="field-row"><span class="field-label">DGPS</span><span class="field-value">{{ ais.positionAccuracyHigh ? 'Yes' : 'No' }}</span></div>
                        }
                        @if (ais.messageType != null) {
                          <div class="field-row"><span class="field-label">Msg Type</span><span class="field-value mono">{{ ais.messageType }}</span></div>
                        }
                      }
                    }
                    @case ('adsb') {
                      @if (e.platformData?.adsb; as adsb) {
                        @if (adsb.aircraftTypeName || adsb.aircraftType) {
                          <div class="field-row"><span class="field-label">Aircraft</span><span class="field-value">{{ adsb.aircraftTypeName || adsb.aircraftType }}</span></div>
                        }
                        @if (adsb.operatorName) {
                          <div class="field-row"><span class="field-label">Operator</span><span class="field-value">{{ adsb.operatorName }}</span></div>
                        }
                        @if (adsb.emergency) {
                          <div class="field-row"><span class="field-label">Emergency</span><span class="field-value chip-damage HEAVY">{{ adsb.emergency }}</span></div>
                        }
                        @if (adsb.altitudeBaro != null) {
                          <div class="field-row"><span class="field-label">Alt (Baro)</span><span class="field-value mono">{{ adsb.altitudeBaro | number:'1.0-0' }} ft</span></div>
                        }
                        @if (adsb.altitudeGeom != null) {
                          <div class="field-row"><span class="field-label">Alt (GPS)</span><span class="field-value mono">{{ adsb.altitudeGeom | number:'1.0-0' }} ft</span></div>
                        }
                        @if (adsb.verticalRate != null) {
                          <div class="field-row"><span class="field-label">Vert Rate</span><span class="field-value mono">{{ adsb.verticalRate | number:'1.1-1' }} m/s</span></div>
                        }
                        @if (adsb.groundSpeed != null) {
                          <div class="field-row"><span class="field-label">Ground Speed</span><span class="field-value mono">{{ adsb.groundSpeed | number:'1.0-0' }} kts</span></div>
                        }
                        @if (adsb.indicatedAirSpeed != null) {
                          <div class="field-row"><span class="field-label">IAS</span><span class="field-value mono">{{ adsb.indicatedAirSpeed | number:'1.0-0' }} kts</span></div>
                        }
                        @if (adsb.trueAirSpeed != null) {
                          <div class="field-row"><span class="field-label">TAS</span><span class="field-value mono">{{ adsb.trueAirSpeed | number:'1.0-0' }} kts</span></div>
                        }
                        @if (adsb.magneticHeading != null) {
                          <div class="field-row"><span class="field-label">Mag Heading</span><span class="field-value mono">{{ adsb.magneticHeading | number:'1.0-0' }}&deg;</span></div>
                        }
                        @if (adsb.onGround != null) {
                          <div class="field-row"><span class="field-label">On Ground</span><span class="field-value">{{ adsb.onGround ? 'Yes' : 'No' }}</span></div>
                        }
                        @if (adsb.category) {
                          <div class="field-row"><span class="field-label">Category</span><span class="field-value mono">{{ adsb.category }}</span></div>
                        }
                      }
                    }
                    @case ('tle') {
                      @if (e.platformData?.tle; as tle) {
                        @if (tle.intlDesignator) {
                          <div class="field-row"><span class="field-label">Intl Desig</span><span class="field-value mono">{{ tle.intlDesignator }}</span></div>
                        }
                        @if (tle.objectType) {
                          <div class="field-row"><span class="field-label">Object Type</span><span class="field-value">{{ tle.objectType }}</span></div>
                        }
                        @if (tle.epoch) {
                          <div class="field-row"><span class="field-label">TLE Epoch</span><span class="field-value mono">{{ tle.epoch | date:'short' }}</span></div>
                        }
                        @if (tle.inclination != null) {
                          <div class="field-row"><span class="field-label">Inclination</span><span class="field-value mono">{{ tle.inclination | number:'1.2-2' }}&deg;</span></div>
                        }
                        @if (tle.eccentricity != null) {
                          <div class="field-row"><span class="field-label">Eccentricity</span><span class="field-value mono">{{ tle.eccentricity | number:'1.6-6' }}</span></div>
                        }
                        @if (tle.period != null) {
                          <div class="field-row"><span class="field-label">Period</span><span class="field-value mono">{{ tle.period | number:'1.1-1' }} min</span></div>
                        }
                        @if (tle.apogee != null) {
                          <div class="field-row"><span class="field-label">Apogee</span><span class="field-value mono">{{ tle.apogee | number:'1.0-0' }} km</span></div>
                        }
                        @if (tle.perigee != null) {
                          <div class="field-row"><span class="field-label">Perigee</span><span class="field-value mono">{{ tle.perigee | number:'1.0-0' }} km</span></div>
                        }
                        @if (tle.meanMotion != null) {
                          <div class="field-row"><span class="field-label">Mean Motion</span><span class="field-value mono">{{ tle.meanMotion | number:'1.4-4' }} rev/day</span></div>
                        }
                        @if (tle.raan != null) {
                          <div class="field-row"><span class="field-label">RAAN</span><span class="field-value mono">{{ tle.raan | number:'1.2-2' }}&deg;</span></div>
                        }
                        @if (tle.rcsSize) {
                          <div class="field-row"><span class="field-label">RCS</span><span class="field-value">{{ tle.rcsSize }}</span></div>
                        }
                        @if (tle.country) {
                          <div class="field-row"><span class="field-label">Country</span><span class="field-value">{{ tle.country }}</span></div>
                        }
                        @if (tle.launchDate) {
                          <div class="field-row"><span class="field-label">Launched</span><span class="field-value mono">{{ tle.launchDate | date:'mediumDate' }}</span></div>
                        }
                      }
                    }
                    @case ('cot') {
                      @if (e.platformData?.cot; as cot) {
                        <div class="field-row"><span class="field-label">CoT Type</span><span class="field-value mono">{{ cot.cotType }}</span></div>
                        @if (cot.how) {
                          <div class="field-row"><span class="field-label">How</span><span class="field-value mono">{{ cot.how }}</span></div>
                        }
                        @if (cot.ce != null) {
                          <div class="field-row"><span class="field-label">CE</span><span class="field-value mono">{{ cot.ce | number:'1.1-1' }} m</span></div>
                        }
                        @if (cot.le != null) {
                          <div class="field-row"><span class="field-label">LE</span><span class="field-value mono">{{ cot.le | number:'1.1-1' }} m</span></div>
                        }
                        @if (cot.staleTime) {
                          <div class="field-row"><span class="field-label">Stale</span><span class="field-value mono">{{ cot.staleTime | date:'short' }}</span></div>
                        }
                        @if (cot.accessControl) {
                          <div class="field-row"><span class="field-label">Access</span><span class="field-value">{{ cot.accessControl }}</span></div>
                        }
                      }
                    }
                    @case ('link16') {
                      @if (e.platformData?.link16; as l16) {
                        <div class="field-row"><span class="field-label">J-Series</span><span class="field-value mono">{{ l16.jSeriesLabel }}</span></div>
                        @if (l16.originatingUnit) {
                          <div class="field-row"><span class="field-label">Orig Unit</span><span class="field-value">{{ l16.originatingUnit }}</span></div>
                        }
                        @if (l16.quality != null) {
                          <div class="field-row"><span class="field-label">Quality</span><span class="field-value mono">{{ l16.quality }}/15</span></div>
                        }
                        @if (l16.forceIdentity) {
                          <div class="field-row"><span class="field-label">Force ID</span><span class="field-value">{{ l16.forceIdentity }}</span></div>
                        }
                        @if (l16.exerciseIndicator) {
                          <div class="field-row"><span class="field-label">Exercise</span><span class="field-value">Yes</span></div>
                        }
                        @if (l16.simulationIndicator) {
                          <div class="field-row"><span class="field-label">Simulation</span><span class="field-value">Yes</span></div>
                        }
                      }
                    }
                    @case ('uav') {
                      @if (e.platformData?.uav; as uav) {
                        @if (uav.make) {
                          <div class="field-row"><span class="field-label">Make</span><span class="field-value">{{ uav.make }}</span></div>
                        }
                        @if (uav.model) {
                          <div class="field-row"><span class="field-label">Model</span><span class="field-value">{{ uav.model }}</span></div>
                        }
                        @if (uav.serialNumber) {
                          <div class="field-row"><span class="field-label">Serial</span><span class="field-value mono">{{ uav.serialNumber }}</span></div>
                        }
                      }
                    }
                  }
                </div>
              }
            </section>
          }

          <!-- Signal Quality (ADS-B only, collapsible) -->
          @if (platformType() === 'adsb' && e.platformData?.adsb; as adsb) {
            @if (adsb.nacP != null || adsb.sil != null || adsb.nic != null) {
              <section class="section">
                <div class="collapsible-header" (click)="toggleSection('signalQuality')">
                  <h4 class="section-label" style="margin:0">Signal Quality</h4>
                  <span class="chevron" [class.expanded]="expandedSections()['signalQuality']">&#9654;</span>
                </div>
                @if (expandedSections()['signalQuality']) {
                  <div class="collapsible-content">
                    @if (adsb.nacP != null) {
                      <div class="field-row"><span class="field-label">NACp</span><span class="field-value mono">{{ adsb.nacP }}</span></div>
                    }
                    @if (adsb.nacV != null) {
                      <div class="field-row"><span class="field-label">NACv</span><span class="field-value mono">{{ adsb.nacV }}</span></div>
                    }
                    @if (adsb.sil != null) {
                      <div class="field-row"><span class="field-label">SIL</span><span class="field-value mono">{{ adsb.sil }}@if (adsb.silType) { ({{ adsb.silType }}) }</span></div>
                    }
                    @if (adsb.nic != null) {
                      <div class="field-row"><span class="field-label">NIC</span><span class="field-value mono">{{ adsb.nic }}</span></div>
                    }
                    @if (adsb.rc != null) {
                      <div class="field-row"><span class="field-label">RC</span><span class="field-value mono">{{ adsb.rc | number:'1.0-0' }} m</span></div>
                    }
                    @if (adsb.gva != null) {
                      <div class="field-row"><span class="field-label">GVA</span><span class="field-value mono">{{ adsb.gva }}</span></div>
                    }
                    @if (adsb.sda != null) {
                      <div class="field-row"><span class="field-label">SDA</span><span class="field-value mono">{{ adsb.sda }}</span></div>
                    }
                  </div>
                }
              </section>
            }
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
      width: var(--panel-width, 600px);
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

    .model-section {
      padding: 12px 0;

      model-viewer {
        width: 100%;
        height: 200px;
        display: block;
        border-radius: var(--radius-md);
        overflow: hidden;
        background-color: var(--bg-tertiary);
        --poster-color: transparent;
      }
    }

    .description-text {
      font-size: 0.8rem;
      color: var(--text-secondary);
      line-height: 1.5;
    }

    .chip-country {
      background: color-mix(in srgb, var(--text-muted) 15%, transparent);
      color: var(--text-secondary);
      border-color: color-mix(in srgb, var(--text-muted) 30%, transparent);
      font-size: 0.65rem;
      font-family: var(--font-mono);
    }

    .chip-affiliation-id {
      font-size: 0.7rem;
      &.friendly {
        background: color-mix(in srgb, #3b82f6 15%, transparent);
        color: #3b82f6;
        border-color: color-mix(in srgb, #3b82f6 30%, transparent);
      }
      &.hostile {
        background: color-mix(in srgb, #ef4444 15%, transparent);
        color: #ef4444;
        border-color: color-mix(in srgb, #ef4444 30%, transparent);
      }
      &.neutral {
        background: color-mix(in srgb, #22c55e 15%, transparent);
        color: #22c55e;
        border-color: color-mix(in srgb, #22c55e 30%, transparent);
      }
      &.unknown {
        background: color-mix(in srgb, var(--text-muted) 15%, transparent);
        color: var(--text-muted);
        border-color: color-mix(in srgb, var(--text-muted) 30%, transparent);
      }
    }

    .chip-op-status {
      font-size: 0.7rem;
      &.OPERATIONAL {
        background: color-mix(in srgb, #22c55e 15%, transparent);
        color: #22c55e;
        border-color: color-mix(in srgb, #22c55e 30%, transparent);
      }
      &.DEGRADED {
        background: color-mix(in srgb, #eab308 15%, transparent);
        color: #eab308;
        border-color: color-mix(in srgb, #eab308 30%, transparent);
      }
      &.DAMAGED {
        background: color-mix(in srgb, #f97316 15%, transparent);
        color: #f97316;
        border-color: color-mix(in srgb, #f97316 30%, transparent);
      }
      &.DESTROYED, &.INACTIVE {
        background: color-mix(in srgb, #ef4444 15%, transparent);
        color: #ef4444;
        border-color: color-mix(in srgb, #ef4444 30%, transparent);
      }
    }

    .chip-damage {
      font-size: 0.7rem;
      &.LIGHT {
        background: color-mix(in srgb, #eab308 15%, transparent);
        color: #eab308;
        border-color: color-mix(in srgb, #eab308 30%, transparent);
      }
      &.MODERATE {
        background: color-mix(in srgb, #f97316 15%, transparent);
        color: #f97316;
        border-color: color-mix(in srgb, #f97316 30%, transparent);
      }
      &.HEAVY, &.DESTROYED {
        background: color-mix(in srgb, #ef4444 15%, transparent);
        color: #ef4444;
        border-color: color-mix(in srgb, #ef4444 30%, transparent);
      }
    }

    .collapsible-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      cursor: pointer;
      padding: 2px 0;
      user-select: none;

      &:hover {
        opacity: 0.8;
      }
    }

    .chevron {
      font-size: 0.7rem;
      color: var(--text-muted);
      transition: transform 150ms ease;

      &.expanded {
        transform: rotate(90deg);
      }
    }

    .collapsible-content {
      padding-top: 8px;
    }
  `],
})
export class EntityDetailPanelComponent {
  private static readonly TYPES_WITH_MODELS = new Set(['AIRCRAFT', 'DRONE', 'VEHICLE', 'VESSEL', 'SATELLITE']);

  private sanitizer = inject(DomSanitizer);

  entity = input<Entity | null>(null);
  close = output<void>();
  flyTo = output<Entity>();

  constructor() {
    effect(() => {
      this.entity();
      this.expandedSections.set({ platformDetails: false, signalQuality: false });
    });
  }

  protected modelSrc = computed(() => {
    const e = this.entity();
    if (!e || !EntityDetailPanelComponent.TYPES_WITH_MODELS.has(e.entityType)) return null;
    return `/assets/models/${e.entityType.toLowerCase()}.glb`;
  });

  protected modelViewerHtml = computed(() => {
    const src = this.modelSrc();
    if (!src) return '';
    return this.sanitizer.bypassSecurityTrustHtml(`
      <!DOCTYPE html>
      <html>
      <head>
        <script type="module" src="https://ajax.googleapis.com/ajax/libs/model-viewer/4.0.0/model-viewer.min.js"></script>
        <style>
          body { margin: 0; overflow: hidden; background: transparent; }
          model-viewer { width: 100%; height: 100vh; display: block; --poster-color: transparent; }
        </style>
      </head>
      <body>
        <model-viewer
          src="${src}"
          auto-rotate
          camera-controls
          interaction-prompt="none"
          shadow-intensity="0"
        ></model-viewer>
      </body>
      </html>
    `);
  });

  protected expandedSections = signal<Record<string, boolean>>({
    platformDetails: false,
    signalQuality: false,
  });

  protected platformType = computed(() => {
    const pd = this.entity()?.platformData;
    if (!pd) return null;
    if (pd.ais) return 'ais' as const;
    if (pd.adsb) return 'adsb' as const;
    if (pd.tle) return 'tle' as const;
    if (pd.link16) return 'link16' as const;
    if (pd.cot) return 'cot' as const;
    if (pd.uav) return 'uav' as const;
    return null;
  });

  protected primaryId = computed(() => {
    const pd = this.entity()?.platformData;
    if (!pd) return null;
    if (pd.ais) return { label: 'MMSI', value: pd.ais.mmsi };
    if (pd.adsb) return { label: 'ICAO', value: pd.adsb.icaoHex };
    if (pd.tle) return { label: 'NORAD', value: String(pd.tle.noradId) };
    if (pd.cot) return { label: 'UID', value: pd.cot.uid };
    if (pd.link16) return { label: 'JTN', value: String(pd.link16.trackNumber) };
    return null;
  });

  protected secondaryIds = computed(() => {
    const pd = this.entity()?.platformData;
    const ids: { label: string; value: string }[] = [];
    if (!pd) return ids;

    if (pd.ais) {
      if (pd.ais.callsign) ids.push({ label: 'Callsign', value: pd.ais.callsign });
      if (pd.ais.imo) ids.push({ label: 'IMO', value: pd.ais.imo });
      if (pd.ais.vesselName) ids.push({ label: 'Vessel', value: pd.ais.vesselName });
    }
    if (pd.adsb) {
      if (pd.adsb.aircraftId) ids.push({ label: 'Callsign', value: pd.adsb.aircraftId });
      if (pd.adsb.registration) ids.push({ label: 'Reg', value: pd.adsb.registration });
      if (pd.adsb.squawk) ids.push({ label: 'Squawk', value: pd.adsb.squawk });
    }
    if (pd.tle?.satName) ids.push({ label: 'Name', value: pd.tle.satName });
    if (pd.link16?.originatingUnit) ids.push({ label: 'Unit', value: pd.link16.originatingUnit });

    return ids;
  });

  protected affiliationColor = computed(() => {
    switch (this.entity()?.affiliation) {
      case 'FRIENDLY': case 'ASSUMED_FRIENDLY': return 'friendly';
      case 'HOSTILE': case 'SUSPECT': return 'hostile';
      case 'NEUTRAL': return 'neutral';
      default: return 'unknown';
    }
  });

  protected toggleSection(key: string): void {
    this.expandedSections.update(s => ({ ...s, [key]: !s[key] }));
  }
}
