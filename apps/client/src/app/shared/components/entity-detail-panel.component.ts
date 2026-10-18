import { Component, computed, CUSTOM_ELEMENTS_SCHEMA, DestroyRef, effect, inject, input, output, signal, untracked } from '@angular/core';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { CommonModule } from '@angular/common';
import { AnalyticsService } from '../../core/services/analytics.service';
import { AuthService } from '../../core/services/auth.service';
import { FeedService } from '../../core/services/feed.service';
import { LocationService } from '../../core/services/location.service';
import { OrbitOverlayService } from '../../core/services/orbit-overlay.service';
import { FlightPathService } from '../../core/services/flight-path.service';
import { FLIGHT_PATH_DAYS } from '../models/flight-path.model';
import { pathSummary } from '../../features/map/flight-path';
import { Entity, EntitySource } from '../models/entity.model';
import { EntityBaseline, EntityPlace, Finding, findingKindLabel, formatDuration, formatMetres } from '../models/finding.model';
import { Location } from '../models/location.model';
import { formatAltitude, formatCoordinate } from '../geo-format';
import { freshnessOf } from '../freshness';
import { VESSEL_CATEGORY_COLORS, VESSEL_CATEGORY_LABELS, formatAisEta, navStatusLabel, vesselCategory } from '../vessel-format';
import { IconComponent } from './icon.component';

/** Sections of the panel that fold; identity is the only one open by default. */
type SectionKey =
  | 'identity'
  | 'behaviour'
  | 'opStatus'
  | 'platformDetails'
  | 'signalQuality'
  | 'model'
  | 'origin'
  | 'affiliations'
  | 'description'
  | 'metadata';

const DEFAULT_SECTIONS: Record<SectionKey, boolean> = {
  identity: true,
  behaviour: false,
  opStatus: false,
  platformDetails: false,
  signalQuality: false,
  model: false,
  origin: false,
  affiliations: false,
  description: false,
  metadata: false,
};

@Component({
  selector: 'app-entity-detail-panel',
  standalone: true,
  imports: [CommonModule, IconComponent],
  schemas: [CUSTOM_ELEMENTS_SCHEMA],
  template: `
    @if (entity(); as e) {
      <aside class="detail-panel">
        <!-- Header: who, what, and how fresh -->
        <div class="panel-header">
          <div class="header-top">
            <h3 class="entity-name" [title]="e.name">{{ e.name }}</h3>
            <div class="header-actions">
              <button type="button" class="icon-btn" (click)="flyTo.emit(e)" title="Go to location" aria-label="Go to location">
                <app-icon name="navigation" />
              </button>
              <button type="button" class="icon-btn" (click)="close.emit()" title="Close" aria-label="Close">
                <app-icon name="x" />
              </button>
            </div>
          </div>
          <div class="header-chips">
            <span class="chips">
              <span class="chip chip-entity-type" [ngClass]="e.entityType">{{ e.entityType }}</span>
              @if (e.affiliation) {
                <span class="chip chip-affiliation-id" [ngClass]="affiliationColor()">{{ e.affiliation }}</span>
              }
              @if (e.countryOfOrigin) {
                <span class="chip chip-country">{{ e.countryOfOrigin }}</span>
              }
            </span>
            <span class="freshness mono" [ngClass]="freshness().state" title="Time since the last fix. Fresh under a minute, aging under ten, stale beyond.">
              <span class="freshness-dot"></span>{{ freshness().label }}
            </span>
          </div>
        </div>

        <div class="classification-banner" [ngClass]="e.classification">{{ e.classification }}</div>

        <div class="panel-body">
          <!-- Key facts: what an operator checks first -->
          <div class="key-facts">
            <div class="fact" data-fact="position">
              <span class="fact-label">Position</span>
              @if (e.position; as p) {
                <span class="fact-value mono">{{ positionText() }}</span>
                @if (e.circularError != null) {
                  <span class="fact-sub mono">CEP {{ e.circularError | number:'1.0-0' }} m</span>
                }
              } @else {
                <span class="fact-value mono">&mdash;</span>
              }
            </div>
            <div class="fact" data-fact="movement">
              <span class="fact-label">Speed &middot; Heading</span>
              @if (e.speedKnots != null || e.heading != null) {
                <span class="fact-value mono">
                  @if (e.speedKnots != null) {
                    <span>{{ e.speedKnots | number:'1.1-1' }} kts</span>
                  }
                  @if (e.heading != null) {
                    <span class="heading">
                      <app-icon name="arrow-up" [size]="12" [strokeWidth]="2.5" class="heading-arrow" [style.transform]="'rotate(' + e.heading + 'deg)'" />
                      {{ headingText() }}
                    </span>
                  }
                </span>
                @if (e.course != null) {
                  <span class="fact-sub mono">course {{ courseText() }}</span>
                }
              } @else {
                <span class="fact-value mono">&mdash;</span>
              }
            </div>
            <div class="fact" data-fact="last-fix">
              <span class="fact-label">Last fix</span>
              <span class="fact-value mono">{{ lastFixAt() | date:'HH:mm:ss':'UTC' }}Z</span>
              <span class="fact-sub mono">{{ e.source }}@if (connectionName(); as name) { &middot; {{ name }}}</span>
            </div>
            <div class="fact" data-fact="altitude">
              <span class="fact-label">Altitude</span>
              @if (e.position?.altitude != null) {
                <span class="fact-value mono">{{ altitudeText() }}</span>
              } @else {
                <span class="fact-value mono">&mdash;</span>
              }
              @if (e.operationalStatus && e.operationalStatus !== 'UNKNOWN') {
                <span class="fact-sub mono">{{ e.operationalStatus | lowercase }}</span>
              }
            </div>
          </div>

          <!-- Open findings, above the fold -->
          @if (findings().length > 0) {
            <div class="open-findings">
              <ul class="finding-list">
                @for (f of findings(); track f.id) {
                  <li class="finding-item" [ngClass]="f.severity.toLowerCase()">
                    <div class="finding-head">
                      <span class="chip chip-severity" [ngClass]="f.severity.toLowerCase()">{{ f.severity }}</span>
                      <span class="finding-kind">{{ kindLabel(f.kind) }}</span>
                      <span class="finding-hits mono">{{ f.hits }}&times;</span>
                    </div>
                    <p class="finding-summary">{{ f.summary }}</p>
                    @if (canReview()) {
                      <div class="finding-actions">
                        <button class="btn btn-secondary btn-sm" (click)="review(f, 'CONFIRMED')">Confirm</button>
                        <button class="btn btn-ghost btn-sm" (click)="review(f, 'DISMISSED')">Dismiss</button>
                      </div>
                    }
                  </li>
                }
              </ul>
            </div>
          }

          <div class="sections">
            <!-- Identity -->
            @if (primaryId() || secondaryIds().length > 0 || e.sourceEntityId) {
              <section class="section" [class.expanded]="isOpen('identity')">
                <button type="button" class="section-toggle" data-section="identity" [attr.aria-expanded]="isOpen('identity')" (click)="toggleSection('identity')">
                  <app-icon name="chevron-right" [size]="12" class="chevron" />
                  <h4 class="section-label">Identity</h4>
                  @if (!isOpen('identity')) {
                    <span class="section-summary mono">{{ identitySummary() }}</span>
                  }
                </button>
                @if (isOpen('identity')) {
                  <div class="section-content">
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
                  </div>
                }
              </section>
            }

            <!-- Behaviour: what is normal for this entity -->
            <section class="section behaviour-section" [class.expanded]="isOpen('behaviour')">
              <button type="button" class="section-toggle" data-section="behaviour" [attr.aria-expanded]="isOpen('behaviour')" (click)="toggleSection('behaviour')">
                <app-icon name="chevron-right" [size]="12" class="chevron" />
                <h4 class="section-label">Behaviour</h4>
                @if (!isOpen('behaviour')) {
                  <span class="section-summary mono">{{ behaviourSummary() }}</span>
                }
              </button>
              @if (isOpen('behaviour')) {
                <div class="section-content">
                  @if (baseline(); as b) {
                    <div class="field-row">
                      <span class="field-label">Baseline</span>
                      <span class="field-value mono">{{ b.pointCount }} fixes &middot; {{ b.days }} d{{ b.mature ? '' : ' &middot; immature' }}</span>
                    </div>
                    @if (b.places.length > 0) {
                      <div class="field-row">
                        <span class="field-label">Range</span>
                        <span class="field-value mono" title="Distance beyond the nearest usual place: median / 95th percentile">{{ fmtM(b.stats.range.p50M) }} / {{ fmtM(b.stats.range.p95M) }}</span>
                      </div>
                    }
                    @if (b.stats.speed.n > 0) {
                      <div class="field-row">
                        <span class="field-label">Speed</span>
                        <span class="field-value mono">{{ b.stats.speed.mean | number:'1.0-0' }} &plusmn; {{ b.stats.speed.stddev | number:'1.0-0' }} kts, p95 {{ b.stats.speed.p95 | number:'1.0-0' }}</span>
                      </div>
                    }
                    @if (b.stats.gap.p95Sec > 0) {
                      <div class="field-row">
                        <span class="field-label">Reports every</span>
                        <span class="field-value mono">{{ fmtSec(b.stats.gap.p50Sec) }} (p95 {{ fmtSec(b.stats.gap.p95Sec) }})</span>
                      </div>
                    }
                    @if (b.places.length > 0) {
                      <div class="field-row-stack">
                        <span class="field-label">Usual places</span>
                        <ul class="place-list">
                          @for (place of topPlaces(b); track place.clusterIndex) {
                            <li class="place-item" (click)="flyToPlace(place)" title="Show on map">
                              <span class="place-name">{{ placeName(place) }}</span>
                              <span class="place-meta mono">{{ place.visits }} visits &middot; {{ fmtSec(place.medianDwellSec) }} &middot; {{ (place.pointShare * 100) | number:'1.0-0' }}%</span>
                            </li>
                          }
                        </ul>
                      </div>
                    }
                    <div class="field-row-stack">
                      <span class="field-label">Active hours <span class="text-muted">(local, Sun&ndash;Sat)</span></span>
                      <div class="hour-grid" role="img" aria-label="Activity by hour of week">
                        @for (cell of hourCells(b); track $index) {
                          <div class="hour-cell" [style.opacity]="cell.opacity" [title]="cell.title"></div>
                        }
                      </div>
                    </div>
                  } @else if (baseline() === null) {
                    <p class="behaviour-note">No baseline yet. Departures are judged once a few days of history exist.</p>
                  } @else {
                    <p class="behaviour-note">Loading&hellip;</p>
                  }
                </div>
              }
            </section>

            <!-- Operational status -->
            @if (e.operationalStatus && e.operationalStatus !== 'UNKNOWN') {
              <section class="section" [class.expanded]="isOpen('opStatus')">
                <button type="button" class="section-toggle" data-section="opStatus" [attr.aria-expanded]="isOpen('opStatus')" (click)="toggleSection('opStatus')">
                  <app-icon name="chevron-right" [size]="12" class="chevron" />
                  <h4 class="section-label">Operational Status</h4>
                  @if (!isOpen('opStatus')) {
                    <span class="section-summary mono">{{ e.operationalStatus | lowercase }}@if (e.damageAssessment && e.damageAssessment !== 'UNKNOWN' && e.damageAssessment !== 'NONE') { &middot; {{ e.damageAssessment | lowercase }} damage}</span>
                  }
                </button>
                @if (isOpen('opStatus')) {
                  <div class="section-content">
                    <div class="field-row">
                      <span class="field-label">Status</span>
                      <span class="chip chip-op-status" [ngClass]="e.operationalStatus">{{ e.operationalStatus }}</span>
                    </div>
                    @if (e.damageAssessment && e.damageAssessment !== 'UNKNOWN' && e.damageAssessment !== 'NONE') {
                      <div class="field-row">
                        <span class="field-label">Damage</span>
                        <span class="chip chip-damage" [ngClass]="e.damageAssessment">{{ e.damageAssessment }}</span>
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
                  </div>
                }
              </section>
            }

            <!-- Platform details -->
            @if (platformType()) {
              <section class="section" [class.expanded]="isOpen('platformDetails')">
                <button type="button" class="section-toggle" data-section="platformDetails" [attr.aria-expanded]="isOpen('platformDetails')" (click)="toggleSection('platformDetails')">
                  <app-icon name="chevron-right" [size]="12" class="chevron" />
                  <h4 class="section-label">
                    @switch (platformType()) {
                      @case ('ais') { Vessel Details }
                      @case ('adsb') { Aircraft Details }
                      @case ('tle') { Satellite Orbit }
                      @case ('cot') { CoT Data }
                      @case ('link16') { Link 16 Data }
                      @case ('uav') { UAV Data }
                    }
                  </h4>
                  @if (!isOpen('platformDetails')) {
                    <span class="section-summary mono">{{ platformSummary() }}</span>
                  }
                </button>
                @if (isOpen('platformDetails')) {
                  <div class="section-content">
                    @switch (platformType()) {
                      @case ('ais') {
                        @if (e.platformData?.ais; as ais) {
                          @if (ais.shipTypeName) {
                            <div class="field-row"><span class="field-label">Ship Type</span><span class="field-value vessel-type"><span class="category-dot" [style.background]="vesselCategoryColor(ais.shipType)"></span>{{ vesselTypeText(ais.shipType, ais.shipTypeName) }}</span></div>
                          }
                          @if (navStatusLabel(ais.navStatus); as status) {
                            <div class="field-row"><span class="field-label">Nav Status</span><span class="field-value">{{ status }}</span></div>
                          }
                          @if (ais.destination) {
                            <div class="field-row"><span class="field-label">Destination</span><span class="field-value">{{ ais.destination }}</span></div>
                          }
                          @if (formatAisEta(ais.eta); as eta) {
                            <div class="field-row"><span class="field-label">ETA</span><span class="field-value mono">{{ eta }}</span></div>
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
                          @if (adsb.icaoHex) {
                            <div class="field-row"><span class="field-label">Flight path</span>
                              <span class="path-range" role="group" aria-label="Flight path range">
                                @for (opt of flightPathDays; track opt.days) {
                                  <button type="button" class="path-range-btn" [class.active]="flightPath.state().days === opt.days" [attr.aria-pressed]="flightPath.state().days === opt.days" (click)="flightPath.setDays(opt.days)">{{ opt.label }}</button>
                                }
                              </span>
                            </div>
                            @if (flightPath.state(); as fp) {
                              @if (fp.days > 0) {
                                <p class="path-note mono">{{ fp.loading ? 'Loading from IntelSky…' : (fp.error ?? flightPathSummary(fp.path)) }}</p>
                              }
                            }
                          }
                        }
                      }
                      @case ('tle') {
                        @if (e.platformData?.tle; as tle) {
                          <div class="orbit-toggles" role="group" aria-label="Orbit overlays">
                            <label class="orbit-toggle"><input type="checkbox" [checked]="orbit.showPath()" (change)="orbit.toggle('path')" /> Orbit path</label>
                            <label class="orbit-toggle"><input type="checkbox" [checked]="orbit.showGroundTrack()" (change)="orbit.toggle('groundTrack')" /> Ground track</label>
                            <label class="orbit-toggle"><input type="checkbox" [checked]="orbit.showFootprint()" (change)="orbit.toggle('footprint')" /> Footprint</label>
                          </div>
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
                          @if (tle.line1 && tle.line2) {
                            <div class="field-row-stack">
                              <span class="field-label">Elements <span class="text-muted">(derived from CelesTrak OMM)</span></span>
                              <pre class="tle-lines mono">{{ tle.line1 }}
{{ tle.line2 }}</pre>
                            </div>
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

            <!-- Signal quality (ADS-B only) -->
            @if (platformType() === 'adsb' && e.platformData?.adsb; as adsb) {
              @if (adsb.nacP != null || adsb.sil != null || adsb.nic != null) {
                <section class="section" [class.expanded]="isOpen('signalQuality')">
                  <button type="button" class="section-toggle" data-section="signalQuality" [attr.aria-expanded]="isOpen('signalQuality')" (click)="toggleSection('signalQuality')">
                    <app-icon name="chevron-right" [size]="12" class="chevron" />
                    <h4 class="section-label">Signal Quality</h4>
                    @if (!isOpen('signalQuality')) {
                      <span class="section-summary mono">@if (adsb.nacP != null) {NACp {{ adsb.nacP }}}@if (adsb.nic != null) { &middot; NIC {{ adsb.nic }}}</span>
                    }
                  </button>
                  @if (isOpen('signalQuality')) {
                    <div class="section-content">
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

            <!-- 3D model, loaded only on demand -->
            @if (modelSrc(); as src) {
              <section class="section" [class.expanded]="isOpen('model')">
                <button type="button" class="section-toggle" data-section="model" [attr.aria-expanded]="isOpen('model')" (click)="toggleSection('model')">
                  <app-icon name="chevron-right" [size]="12" class="chevron" />
                  <h4 class="section-label">3D Model</h4>
                  @if (!isOpen('model')) {
                    <span class="section-summary mono">{{ modelFile() }}</span>
                  }
                </button>
                @if (isOpen('model')) {
                  <div class="section-content model-section">
                    <iframe
                      title="3D model"
                      [srcdoc]="modelViewerHtml()"
                      style="width:100%;height:200px;display:block;border:none;border-radius:8px;background:transparent"
                    ></iframe>
                  </div>
                }
              </section>
            }

            <!-- Origin & timing -->
            <section class="section origin-section" [class.expanded]="isOpen('origin')">
              <button type="button" class="section-toggle" data-section="origin" [attr.aria-expanded]="isOpen('origin')" (click)="toggleSection('origin')">
                <app-icon name="chevron-right" [size]="12" class="chevron" />
                <h4 class="section-label">Origin & Timing</h4>
                @if (!isOpen('origin')) {
                  <span class="section-summary mono">{{ e.source }} &middot; since {{ e.createdAt | date:'yyyy-MM-dd' }}</span>
                }
              </button>
              @if (isOpen('origin')) {
                <div class="section-content">
                  @if (connectionName(); as name) {
                    <div class="field-row">
                      <span class="field-label">Connection</span>
                      <span class="field-value" [title]="e.feedId ?? ''">{{ name }}</span>
                    </div>
                  }
                  <div class="field-row">
                    <span class="field-label">Source</span>
                    <span class="chip chip-source">{{ e.source }}</span>
                  </div>
                  @if (cotUid(); as uid) {
                    <div class="field-row">
                      <span class="field-label">TAK uid</span>
                      <span class="field-value mono" [title]="uid">{{ uid }}</span>
                    </div>
                    <div class="field-row">
                      <span class="field-label">Callsign</span>
                      <span class="field-value mono">{{ cotCallsign() }}</span>
                    </div>
                  }
                  @if (contributingFeeds().length > 0) {
                    <div class="field-row field-row-stack">
                      <span class="field-label">Contributing feeds</span>
                      <ul class="source-list">
                        @for (s of contributingFeeds(); track s.feedId + '/' + s.source) {
                          <li class="mono" [title]="s.lastObservedAt ?? ''">
                            <span>{{ feedName(s.feedId) }}</span>
                            <span class="source-tag">{{ s.source }}</span>
                          </li>
                        }
                      </ul>
                    </div>
                  }
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
                </div>
              }
            </section>

            <!-- Affiliations -->
            @if (e.affiliations.length > 0) {
              <section class="section" [class.expanded]="isOpen('affiliations')">
                <button type="button" class="section-toggle" data-section="affiliations" [attr.aria-expanded]="isOpen('affiliations')" (click)="toggleSection('affiliations')">
                  <app-icon name="chevron-right" [size]="12" class="chevron" />
                  <h4 class="section-label">Affiliations</h4>
                  @if (!isOpen('affiliations')) {
                    <span class="section-summary mono">{{ e.affiliations.length }} listed</span>
                  }
                </button>
                @if (isOpen('affiliations')) {
                  <div class="section-content">
                    <div class="chip-list">
                      @for (aff of e.affiliations; track aff) {
                        <span class="chip chip-affiliation">{{ aff }}</span>
                      }
                    </div>
                  </div>
                }
              </section>
            }

            <!-- Description -->
            @if (e.description) {
              <section class="section" [class.expanded]="isOpen('description')">
                <button type="button" class="section-toggle" data-section="description" [attr.aria-expanded]="isOpen('description')" (click)="toggleSection('description')">
                  <app-icon name="chevron-right" [size]="12" class="chevron" />
                  <h4 class="section-label">Description</h4>
                  @if (!isOpen('description')) {
                    <span class="section-summary">{{ e.description }}</span>
                  }
                </button>
                @if (isOpen('description')) {
                  <div class="section-content">
                    <p class="description-text">{{ e.description }}</p>
                  </div>
                }
              </section>
            }

            <!-- Metadata -->
            @if (e.metadata | keyvalue; as kvs) {
              @if (kvs.length > 0) {
                <section class="section" [class.expanded]="isOpen('metadata')">
                  <button type="button" class="section-toggle" data-section="metadata" [attr.aria-expanded]="isOpen('metadata')" (click)="toggleSection('metadata')">
                    <app-icon name="chevron-right" [size]="12" class="chevron" />
                    <h4 class="section-label">Metadata</h4>
                    @if (!isOpen('metadata')) {
                      <span class="section-summary mono">{{ kvs.length }} {{ kvs.length === 1 ? 'field' : 'fields' }}</span>
                    }
                  </button>
                  @if (isOpen('metadata')) {
                    <div class="section-content">
                      @for (kv of kvs; track kv.key) {
                        <div class="field-row">
                          <span class="field-label">{{ kv.key }}</span>
                          <span class="field-value mono">{{ kv.value }}</span>
                        </div>
                      }
                    </div>
                  }
                </section>
              }
            }
          </div>
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

    /* --- Header --- */
    .panel-header {
      display: flex;
      flex-direction: column;
      gap: 8px;
      padding: 14px 16px 12px;
    }

    .header-top {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
    }

    .entity-name {
      font-size: 1rem;
      font-weight: 600;
      color: var(--text-primary);
      margin: 0;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .header-actions {
      display: flex;
      gap: 2px;
      flex-shrink: 0;
    }

    .icon-btn {
      width: 28px;
      height: 28px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      border-radius: var(--radius-sm);
      background: transparent;
      color: var(--text-secondary);
      transition: all var(--transition-fast);

      &:hover {
        background: color-mix(in srgb, var(--text-muted) 15%, transparent);
        color: var(--text-primary);
      }
    }

    .header-chips {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
    }

    .chips {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      flex-wrap: wrap;
      min-width: 0;
    }

    .freshness {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 2px 8px 2px 6px;
      border-radius: 100px;
      font-size: 0.7rem;
      font-weight: 600;
      white-space: nowrap;
      flex-shrink: 0;

      &.fresh { background: color-mix(in srgb, var(--accent-green) 12%, transparent); color: var(--accent-green); }
      &.aging { background: color-mix(in srgb, var(--accent-yellow) 12%, transparent); color: var(--accent-yellow); }
      &.stale { background: color-mix(in srgb, var(--accent-red) 12%, transparent); color: var(--accent-red); }
    }

    .freshness-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: currentColor;
    }

    /* --- Classification banner --- */
    .classification-banner {
      height: 20px;
      display: flex;
      align-items: center;
      justify-content: center;
      font-family: var(--font-mono);
      font-size: 0.65rem;
      font-weight: 700;
      letter-spacing: 0.12em;
      text-transform: uppercase;
      border-top: 1px solid transparent;
      border-bottom: 1px solid transparent;

      &.UNCLASSIFIED { background: color-mix(in srgb, var(--accent-green) 12%, transparent); color: var(--accent-green); border-color: color-mix(in srgb, var(--accent-green) 25%, transparent); }
      &.CONFIDENTIAL { background: color-mix(in srgb, var(--accent-blue) 12%, transparent); color: var(--accent-blue); border-color: color-mix(in srgb, var(--accent-blue) 25%, transparent); }
      &.SECRET { background: color-mix(in srgb, var(--accent-red) 12%, transparent); color: var(--accent-red); border-color: color-mix(in srgb, var(--accent-red) 25%, transparent); }
      &.TOP_SECRET { background: color-mix(in srgb, var(--accent-yellow) 12%, transparent); color: var(--accent-yellow); border-color: color-mix(in srgb, var(--accent-yellow) 25%, transparent); }
    }

    .panel-body {
      flex: 1;
      overflow-y: auto;
      padding: 12px 16px 16px;
      display: flex;
      flex-direction: column;
      gap: 12px;
    }

    /* --- Key facts --- */
    .key-facts {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
    }

    .fact {
      padding: 10px 12px;
      border-radius: var(--radius-md);
      background: var(--bg-primary);
      border: 1px solid var(--border-color);
      display: flex;
      flex-direction: column;
      gap: 2px;
      min-width: 0;
    }

    .fact-label {
      font-family: var(--font-mono);
      font-size: 0.65rem;
      letter-spacing: 0.1em;
      text-transform: uppercase;
      color: var(--text-muted);
    }

    .fact-value {
      font-size: 0.85rem;
      color: var(--text-primary);
      line-height: 1.35;
      white-space: pre-line;
      display: flex;
      align-items: center;
      gap: 8px;
      flex-wrap: wrap;
    }

    .heading {
      display: inline-flex;
      align-items: center;
      gap: 3px;
    }

    .heading-arrow {
      color: var(--accent-cyan);
    }

    .fact-sub {
      font-size: 0.65rem;
      color: var(--text-muted);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    /* --- Sections --- */
    .sections {
      display: flex;
      flex-direction: column;
      border-top: 1px solid var(--border-color);
    }

    .section {
      border-bottom: 1px solid var(--border-color);

      &:last-child {
        border-bottom: none;
      }
    }

    .section-toggle {
      width: 100%;
      display: flex;
      align-items: center;
      gap: 8px;
      min-height: 36px;
      padding: 4px 0;
      background: transparent;
      color: var(--text-secondary);
      text-align: left;
      cursor: pointer;

      &:hover .section-label {
        color: var(--text-primary);
      }

      .chevron {
        color: var(--text-muted);
        transition: transform 150ms ease;
      }
    }

    .section.expanded .section-toggle .chevron {
      transform: rotate(90deg);
    }

    .section-label {
      font-size: 0.7rem;
      font-weight: 600;
      color: var(--text-secondary);
      text-transform: uppercase;
      letter-spacing: 0.06em;
      margin: 0;
      flex-shrink: 0;
    }

    .section-summary {
      margin-left: auto;
      font-size: 0.7rem;
      color: var(--text-muted);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      min-width: 0;
      text-align: right;
    }

    .section-content {
      padding: 0 0 10px 20px;
    }

    .field-row {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 3px 0;
      gap: 12px;
    }

    .field-label {
      font-size: 0.8rem;
      color: var(--text-muted);
      flex-shrink: 0;
    }

    .vessel-type {
      display: inline-flex;
      align-items: center;
      gap: 6px;
    }

    .category-dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
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

    .behaviour-note {
      margin: 0;
      font-size: 0.78rem;
      color: var(--text-muted);
    }

    .path-range {
      display: inline-flex;
      gap: 2px;
    }

    .path-range-btn {
      padding: 1px 7px;
      font-size: 0.72rem;
      color: var(--text-secondary);
      background: transparent;
      border: 1px solid var(--border-color, rgba(148, 163, 184, 0.3));
      border-radius: 4px;
      cursor: pointer;

      &.active {
        color: #fff;
        background: #f97316;
        border-color: #f97316;
      }
    }

    .path-note {
      margin: 0 0 6px;
      font-size: 0.72rem;
      color: var(--text-muted);
      text-align: right;
    }

    .orbit-toggles {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      margin: 0 0 8px;
    }

    .orbit-toggle {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      font-size: 0.78rem;
      color: var(--text-secondary);
      cursor: pointer;
    }

    .tle-lines {
      margin: 4px 0 0;
      padding: 6px 8px;
      border-radius: 4px;
      background: var(--bg-primary);
      font-size: 0.66rem;
      line-height: 1.4;
      white-space: pre;
      overflow-x: auto;
      color: var(--text-secondary);
    }

    .place-list {
      list-style: none;
      margin: 4px 0 0;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: 2px;
    }

    .place-item {
      display: flex;
      justify-content: space-between;
      gap: 8px;
      padding: 3px 6px;
      border-radius: 4px;
      font-size: 0.78rem;
      cursor: pointer;

      &:hover {
        background: var(--bg-primary);
      }
    }

    .place-name {
      color: var(--text-secondary);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .place-meta {
      color: var(--text-muted);
      font-size: 0.72rem;
      flex-shrink: 0;
    }

    .hour-grid {
      display: grid;
      grid-template-columns: repeat(24, 1fr);
      gap: 1px;
      margin-top: 4px;
    }

    .hour-cell {
      aspect-ratio: 1;
      min-height: 6px;
      border-radius: 1px;
      background: var(--accent-blue);
    }

    /* --- Findings --- */
    .finding-list {
      list-style: none;
      margin: 0;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: 6px;
    }

    .finding-item {
      padding: 8px 10px;
      border: 1px solid color-mix(in srgb, var(--text-muted) 35%, transparent);
      border-radius: var(--radius-md);
      background: color-mix(in srgb, var(--text-muted) 7%, transparent);

      &.critical { border-color: color-mix(in srgb, var(--accent-red) 35%, transparent); background: color-mix(in srgb, var(--accent-red) 7%, transparent); }
      &.high { border-color: color-mix(in srgb, var(--accent-orange) 35%, transparent); background: color-mix(in srgb, var(--accent-orange) 7%, transparent); }
      &.medium { border-color: color-mix(in srgb, var(--accent-yellow) 35%, transparent); background: color-mix(in srgb, var(--accent-yellow) 7%, transparent); }
      &.low { border-color: color-mix(in srgb, var(--accent-blue) 35%, transparent); background: color-mix(in srgb, var(--accent-blue) 7%, transparent); }
    }

    .finding-head {
      display: flex;
      align-items: center;
      gap: 6px;
    }

    .chip-severity {
      font-size: 0.64rem;
      font-weight: 700;
      letter-spacing: 0.06em;
      background: color-mix(in srgb, var(--text-muted) 20%, transparent);
      color: var(--text-muted);

      &.critical { background: color-mix(in srgb, var(--accent-red) 20%, transparent); color: var(--accent-red); }
      &.high { background: color-mix(in srgb, var(--accent-orange) 20%, transparent); color: var(--accent-orange); }
      &.medium { background: color-mix(in srgb, var(--accent-yellow) 20%, transparent); color: var(--accent-yellow); }
      &.low { background: color-mix(in srgb, var(--accent-blue) 20%, transparent); color: var(--accent-blue); }
    }

    .finding-kind {
      font-size: 0.78rem;
      font-weight: 600;
      color: var(--text-primary);
    }

    .finding-hits {
      margin-left: auto;
      font-size: 0.7rem;
      color: var(--text-muted);
    }

    .finding-summary {
      margin: 4px 0 0;
      font-size: 0.76rem;
      color: var(--text-secondary);
      line-height: 1.35;
    }

    .finding-actions {
      display: flex;
      gap: 6px;
      margin-top: 6px;
    }

    .chip-list {
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
    }

    .field-row-stack {
      flex-direction: column;
      align-items: stretch;
      gap: 4px;
    }

    .source-list {
      list-style: none;
      margin: 0;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: 2px;

      li {
        display: flex;
        justify-content: space-between;
        gap: 8px;
        font-size: 0.75rem;
        color: var(--text-secondary);
      }
    }

    .source-tag {
      color: var(--text-muted);
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
      padding-left: 0;
    }

    .description-text {
      margin: 0;
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
        background: color-mix(in srgb, var(--accent-blue) 15%, transparent);
        color: var(--accent-blue);
        border-color: color-mix(in srgb, var(--accent-blue) 30%, transparent);
      }
      &.hostile {
        background: color-mix(in srgb, var(--accent-red) 15%, transparent);
        color: var(--accent-red);
        border-color: color-mix(in srgb, var(--accent-red) 30%, transparent);
      }
      &.neutral {
        background: color-mix(in srgb, var(--accent-green) 15%, transparent);
        color: var(--accent-green);
        border-color: color-mix(in srgb, var(--accent-green) 30%, transparent);
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
        background: color-mix(in srgb, var(--accent-green) 15%, transparent);
        color: var(--accent-green);
        border-color: color-mix(in srgb, var(--accent-green) 30%, transparent);
      }
      &.DEGRADED {
        background: color-mix(in srgb, var(--accent-yellow) 15%, transparent);
        color: var(--accent-yellow);
        border-color: color-mix(in srgb, var(--accent-yellow) 30%, transparent);
      }
      &.DAMAGED {
        background: color-mix(in srgb, var(--accent-orange) 15%, transparent);
        color: var(--accent-orange);
        border-color: color-mix(in srgb, var(--accent-orange) 30%, transparent);
      }
      &.DESTROYED, &.INACTIVE {
        background: color-mix(in srgb, var(--accent-red) 15%, transparent);
        color: var(--accent-red);
        border-color: color-mix(in srgb, var(--accent-red) 30%, transparent);
      }
    }

    .chip-damage {
      font-size: 0.7rem;
      &.LIGHT {
        background: color-mix(in srgb, var(--accent-yellow) 15%, transparent);
        color: var(--accent-yellow);
        border-color: color-mix(in srgb, var(--accent-yellow) 30%, transparent);
      }
      &.MODERATE {
        background: color-mix(in srgb, var(--accent-orange) 15%, transparent);
        color: var(--accent-orange);
        border-color: color-mix(in srgb, var(--accent-orange) 30%, transparent);
      }
      &.HEAVY, &.DESTROYED {
        background: color-mix(in srgb, var(--accent-red) 15%, transparent);
        color: var(--accent-red);
        border-color: color-mix(in srgb, var(--accent-red) 30%, transparent);
      }
    }
  `],
})
export class EntityDetailPanelComponent {
  private static readonly TYPES_WITH_MODELS = new Set(['AIRCRAFT', 'DRONE', 'VEHICLE', 'VESSEL', 'SATELLITE']);

  private sanitizer = inject(DomSanitizer);
  private readonly feedService = inject(FeedService);
  private readonly analytics = inject(AnalyticsService);
  private readonly auth = inject(AuthService);
  private readonly locations = inject(LocationService);
  protected readonly orbit = inject(OrbitOverlayService);
  protected readonly flightPath = inject(FlightPathService);
  protected readonly flightPathDays = FLIGHT_PATH_DAYS;
  protected readonly flightPathSummary = pathSummary;
  private readonly destroyRef = inject(DestroyRef);
  private feedsRequested = false;
  private behaviourRequest = 0;

  /** undefined while loading, null when the service has no baseline yet. */
  protected baseline = signal<EntityBaseline | null | undefined>(undefined);
  protected findings = signal<Finding[]>([]);
  protected canReview = computed(() => this.auth.hasAnyRole('sentinel-analyst', 'sentinel-admin'));

  /** Wall clock, ticking each second, so the freshness label keeps counting. */
  private readonly now = signal(Date.now());

  entity = input<Entity | null>(null);
  close = output<void>();
  flyTo = output<Entity>();

  constructor() {
    const tick = setInterval(() => this.now.set(Date.now()), 1000);
    this.destroyRef.onDestroy(() => clearInterval(tick));

    effect(() => {
      const e = this.entity();
      // A satellite's platform section holds the orbit toggles, a vessel's its
      // voyage and a military aircraft's its flight path: open them.
      const pd = e?.platformData;
      this.expandedSections.set({ ...DEFAULT_SECTIONS, platformDetails: !!(pd?.tle || pd?.ais || (pd?.adsb && e?.source === EntitySource.INTELSKY)) });
    });
    // Behaviour: the baseline and open findings of the shown entity. A
    // request counter drops answers for an entity that is no longer shown.
    effect(() => {
      const id = this.entity()?.id;
      const request = ++this.behaviourRequest;
      this.baseline.set(undefined);
      this.findings.set([]);
      if (!id) return;
      untracked(() => {
        this.analytics.getBaseline(id).subscribe({
          next: (b) => { if (request === this.behaviourRequest) this.baseline.set(b); },
          error: () => { if (request === this.behaviourRequest) this.baseline.set(null); },
        });
        this.analytics.getEntityFindings(id, { status: 'OPEN', pageSize: 10 }).subscribe({
          next: (res) => { if (request === this.behaviourRequest) this.findings.set(res.data ?? []); },
          error: () => undefined,
        });
      });
    });
    // The connection name comes from the feed list; fetch it once, the
    // first time an entity with a feed id is shown.
    effect(() => {
      const e = this.entity();
      if (e?.feedId && !this.feedService.hasLoaded && !this.feedsRequested) {
        this.feedsRequested = true;
        untracked(() => this.feedService.load().subscribe({ error: () => undefined }));
      }
    });
  }

  // --- Header -------------------------------------------------------------

  protected freshness = computed(() => {
    const e = this.entity();
    return e ? freshnessOf(e, this.now()) : { state: 'stale' as const, label: '' };
  });

  // --- Key facts ----------------------------------------------------------

  protected positionText = computed(() => {
    const p = this.entity()?.position;
    return p ? `${formatCoordinate(p.latitude, 'lat')}\n${formatCoordinate(p.longitude, 'lon')}` : '';
  });

  protected altitudeText = computed(() => formatAltitude(this.entity()?.position?.altitude));
  protected headingText = computed(() => this.degrees(this.entity()?.heading));
  protected courseText = computed(() => this.degrees(this.entity()?.course));

  /** The time of the last fix, falling back to the last update. */
  protected lastFixAt = computed(() => {
    const e = this.entity();
    return e?.lastSeenAt ?? e?.updatedAt ?? null;
  });

  private degrees(value: number | undefined): string {
    if (value == null) return '';
    return `${String(Math.round(((value % 360) + 360) % 360)).padStart(3, '0')}°`;
  }

  // --- Sections -----------------------------------------------------------

  protected expandedSections = signal<Record<SectionKey, boolean>>({ ...DEFAULT_SECTIONS });

  protected isOpen(key: SectionKey): boolean {
    return this.expandedSections()[key];
  }

  protected toggleSection(key: SectionKey): void {
    this.expandedSections.update((s) => ({ ...s, [key]: !s[key] }));
  }

  protected identitySummary = computed(() => {
    const pid = this.primaryId();
    if (pid) return `${pid.label} ${pid.value}`;
    const first = this.secondaryIds()[0];
    if (first) return `${first.label} ${first.value}`;
    return this.entity()?.sourceEntityId ?? '';
  });

  protected behaviourSummary = computed(() => {
    const b = this.baseline();
    if (b === undefined) return 'Loading…';
    if (b === null) return 'No baseline yet';
    const open = this.findings().length;
    return `${b.days} d baseline${open > 0 ? ` · ${open} open` : ''}`;
  });

  protected platformSummary = computed(() => {
    const pd = this.entity()?.platformData;
    if (!pd) return '';
    const parts: (string | undefined | null)[] = [];
    if (pd.ais) parts.push(pd.ais.shipTypeName ? VESSEL_CATEGORY_LABELS[vesselCategory(pd.ais.shipType)] : undefined, navStatusLabel(pd.ais.navStatus), pd.ais.destination ? `to ${pd.ais.destination}` : undefined);
    else if (pd.adsb) parts.push(pd.adsb.aircraftTypeName || pd.adsb.aircraftType, pd.adsb.operatorName);
    else if (pd.tle) parts.push(pd.tle.objectType, pd.tle.country);
    else if (pd.cot) parts.push(pd.cot.cotType);
    else if (pd.link16) parts.push(pd.link16.jSeriesLabel);
    else if (pd.uav) parts.push(pd.uav.make, pd.uav.model);
    return parts.filter((p): p is string => typeof p === 'string' && p !== '').join(' · ');
  });

  protected modelFile = computed(() => this.modelSrc()?.split('/').pop() ?? '');

  // --- Behaviour ----------------------------------------------------------

  protected fmtM(m: number): string {
    return formatMetres(m);
  }

  protected fmtSec(sec: number): string {
    return formatDuration(sec);
  }

  protected kindLabel(kind: string): string {
    return findingKindLabel(kind);
  }

  protected topPlaces(b: EntityBaseline): EntityPlace[] {
    return b.places.slice(0, 5);
  }

  protected placeName(place: EntityPlace): string {
    return place.name ?? `Place ${place.clusterIndex + 1}`;
  }

  /** The 168 hour-of-week slots as cells shaded by their share of activity. */
  protected hourCells(b: EntityBaseline): { opacity: number; title: string }[] {
    const hours = b.hours ?? [];
    const max = Math.max(1, ...hours);
    const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
    return hours.map((count, slot) => ({
      opacity: count > 0 ? 0.15 + 0.85 * (count / max) : 0.04,
      title: `${days[Math.floor(slot / 24)]} ${String(slot % 24).padStart(2, '0')}:00 — ${count} fixes`,
    }));
  }

  protected flyToPlace(place: EntityPlace): void {
    this.locations.flyTo({
      id: `place-${place.clusterIndex}`,
      name: this.placeName(place),
      latitude: place.lat,
      longitude: place.lon,
      altitude: Math.max(1500, place.radiusM * 20),
      heading: 0,
      pitch: -60,
      range: Math.max(2000, place.radiusM * 30),
      has3dTiles: false,
      category: 'CUSTOM',
      createdAt: place.firstSeen,
      updatedAt: place.lastSeen,
    } as Location);
  }

  protected review(f: Finding, status: 'CONFIRMED' | 'DISMISSED'): void {
    this.analytics.reviewFinding(f.id, status).subscribe({
      next: () => this.findings.set(this.findings().filter((x) => x.id !== f.id)),
      error: () => undefined,
    });
  }

  // --- Origin -------------------------------------------------------------

  /** The connection the entity came from, by name when the feed list knows it. */
  protected connectionName = computed(() => {
    const feedId = this.entity()?.feedId;
    if (!feedId) return null;
    return this.feedService.feed(feedId)?.name ?? feedId;
  });

  protected feedName(feedId: string): string {
    return this.feedService.feed(feedId)?.name ?? feedId;
  }

  /** The Cursor on Target uid, for entities that came through TAK. */
  protected cotUid = computed(() => {
    const identities = this.entity()?.identities;
    if (!Array.isArray(identities)) return null;
    const id = identities.find((i) => i?.type === 'COT_UID');
    return id?.value ? String(id.value) : null;
  });

  protected cotCallsign = computed(() => {
    const e = this.entity();
    if (!e) return null;
    const fromPlatformData = e.platformData?.cot?.callsign;
    if (typeof fromPlatformData === 'string' && fromPlatformData !== '') return fromPlatformData;
    const fromPlatforms = e.platforms?.['cot']?.['callsign'];
    if (typeof fromPlatforms === 'string' && fromPlatforms !== '') return fromPlatforms;
    return e.name;
  });

  /** Every feed that fed this entity, when there is more than one. */
  protected contributingFeeds = computed(() => {
    const sources = this.entity()?.sources;
    if (!Array.isArray(sources) || sources.length < 2) return [];
    return sources.filter((s) => s && typeof s.feedId === 'string');
  });

  // --- Model --------------------------------------------------------------

  protected modelSrc = computed(() => {
    const e = this.entity();
    if (!e || !EntityDetailPanelComponent.TYPES_WITH_MODELS.has(e.entityType)) return null;
    return `/assets/models/${e.entityType.toLowerCase()}.glb`;
  });

  protected modelViewerHtml = computed<SafeHtml | string>(() => {
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

  // --- Identity -----------------------------------------------------------

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

  // --- Vessel ---------------------------------------------------------------

  protected readonly navStatusLabel = navStatusLabel;
  protected readonly formatAisEta = formatAisEta;

  protected vesselCategoryColor(shipType: number | undefined): string {
    return VESSEL_CATEGORY_COLORS[vesselCategory(shipType)];
  }

  /** "Cargo", or "Service / special · Tug" when the type says more than its category. */
  protected vesselTypeText(shipType: number | undefined, shipTypeName: string): string {
    const category = VESSEL_CATEGORY_LABELS[vesselCategory(shipType)];
    return shipTypeName && shipTypeName !== category ? `${category} · ${shipTypeName}` : category;
  }

  protected affiliationColor = computed(() => {
    switch (this.entity()?.affiliation) {
      case 'FRIENDLY': case 'ASSUMED_FRIENDLY': return 'friendly';
      case 'HOSTILE': case 'SUSPECT': return 'hostile';
      case 'NEUTRAL': return 'neutral';
      default: return 'unknown';
    }
  });
}
