import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { signal } from '@angular/core';
import { of } from 'rxjs';
import { Router } from '@angular/router';
import { EmplacementService } from '../../core/services/emplacement.service';
import { EntityDetailPanelComponent } from './entity-detail-panel.component';
import { AnalyticsService } from '../../core/services/analytics.service';
import { AuthService } from '../../core/services/auth.service';
import { LocationService } from '../../core/services/location.service';
import { EntityBaseline, Finding } from '../models/finding.model';
import { FlightPathService } from '../../core/services/flight-path.service';
import { OrbitOverlayService } from '../../core/services/orbit-overlay.service';
import { Classification, Entity, EntitySource, EntityType } from '../models/entity.model';
import { Feed } from '../models/feed.model';

function makeEntity(over: Partial<Entity> = {}): Entity {
  return {
    id: 'e1',
    entityType: EntityType.UNIT,
    name: 'ALPHA 1',
    source: 'COT' as EntitySource,
    classification: Classification.UNCLASSIFIED,
    metadata: {},
    affiliations: [],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    ...over,
  };
}

function makeFeed(id: string, name: string): Feed {
  return {
    id,
    name,
    description: '',
    driver: 'tak',
    source: 'COT',
    builtin: false,
    enabled: true,
    version: 1,
    config: {},
    secretRefs: {},
    capabilities: [],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    status: {
      owner: '',
      state: 'running',
      health: 'healthy',
      lastMessageAt: null,
      lastSuccessAt: null,
      messagesTotal: 0,
      entitiesLast: 0,
      errorsTotal: 0,
      appliedVersion: 1,
      detail: {},
      updatedAt: null,
    },
  };
}

const NOW = Date.parse('2026-10-17T14:32:20Z');
const secondsAgo = (s: number) => new Date(NOW - s * 1000).toISOString();

describe('EntityDetailPanelComponent', () => {
  let fixture: ComponentFixture<EntityDetailPanelComponent>;
  let el: HTMLElement;
  let httpMock: HttpTestingController;
  let nowSpy: jest.SpyInstance<number, []>;

  /** Expands a collapsed section by its toggle. */
  function open(section: string): void {
    (el.querySelector(`.section-toggle[data-section="${section}"]`) as HTMLButtonElement).click();
    fixture.detectChanges();
  }

  function rows(): Record<string, string> {
    const out: Record<string, string> = {};
    el.querySelectorAll('.origin-section .field-row').forEach((row) => {
      const label = row.querySelector('.field-label')?.textContent?.trim() ?? '';
      out[label] = (row.querySelector('.field-value, .chip, .source-list')?.textContent ?? '').replace(/\s+/g, ' ').trim();
    });
    return out;
  }

  const analytics = {
    baseline: null as EntityBaseline | null,
    findings: [] as Finding[],
    getBaseline: jest.fn(),
    getEntityFindings: jest.fn(),
    reviewFinding: jest.fn(),
    openCount: signal<number | null>(0),
  };
  const auth = { hasAnyRole: jest.fn().mockReturnValue(false) };
  const locations = { flyTo: jest.fn() };

  beforeEach(async () => {
    analytics.baseline = null;
    analytics.findings = [];
    analytics.getBaseline.mockImplementation(() => of(analytics.baseline));
    analytics.getEntityFindings.mockImplementation(() => of({ data: analytics.findings, total: analytics.findings.length, page: 1, pageSize: 10 }));
    analytics.reviewFinding.mockImplementation((id: string, status: string) => of({ id, status }));
    auth.hasAnyRole.mockReturnValue(false);
    locations.flyTo.mockClear();
    nowSpy = jest.spyOn(Date, 'now').mockReturnValue(NOW);
    await TestBed.configureTestingModule({
      imports: [EntityDetailPanelComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AnalyticsService, useValue: analytics },
        { provide: AuthService, useValue: auth },
        { provide: LocationService, useValue: locations },
      ],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(EntityDetailPanelComponent);
    el = fixture.nativeElement as HTMLElement;
  });

  afterEach(() => {
    httpMock.verify();
    nowSpy.mockRestore();
  });

  it('shows the origin rows for an entity that came through TAK', () => {
    fixture.componentRef.setInput(
      'entity',
      makeEntity({
        feedId: 'tak-hq',
        identities: [{ type: 'COT_UID', value: 'ANDROID-1234' }],
        platformData: { cot: { uid: 'ANDROID-1234', cotType: 'a-f-G-U-C', callsign: 'VIPER' } },
        sources: [
          { feedId: 'tak-hq', source: 'COT' },
          { feedId: 'opensky', source: 'OPENSKY', lastObservedAt: '2026-10-01T00:00:00Z' },
        ],
      }),
    );
    fixture.detectChanges();
    open('origin');

    expect(el.querySelector('.origin-section .section-label')?.textContent).toBe('Origin & Timing');
    // the feed list is fetched once so the connection can be named
    expect(rows()['Connection']).toBe('tak-hq');
    httpMock.expectOne('/api/v1/feeds').flush([makeFeed('tak-hq', 'TAK HQ'), makeFeed('opensky', 'OpenSky')]);
    fixture.detectChanges();

    const r = rows();
    expect(r['Connection']).toBe('TAK HQ');
    expect(r['Source']).toBe('COT');
    expect(r['TAK uid']).toBe('ANDROID-1234');
    expect(r['Callsign']).toBe('VIPER');
    const contributing = Array.from(el.querySelectorAll('.source-list li')).map((li) =>
      Array.from(li.querySelectorAll('span')).map((span) => span.textContent?.trim()),
    );
    expect(contributing).toEqual([
      ['TAK HQ', 'COT'],
      ['OpenSky', 'OPENSKY'],
    ]);
    expect(el.querySelector('.classification-banner')?.textContent?.trim()).toBe('UNCLASSIFIED');
  });

  it('falls back to platforms.cot.callsign, then the name, and hides what it does not have', () => {
    fixture.componentRef.setInput(
      'entity',
      makeEntity({
        identities: [{ type: 'COT_UID', value: 'U-1' }],
        platforms: { cot: { callsign: 'HAWK' } },
        sources: [{ feedId: 'tak-hq', source: 'COT' }],
      }),
    );
    fixture.detectChanges();
    open('origin');

    let r = rows();
    expect(r['Connection']).toBeUndefined();
    expect(r['Callsign']).toBe('HAWK');
    expect(r['Contributing feeds']).toBeUndefined();

    fixture.componentRef.setInput('entity', makeEntity({ id: 'e2', identities: [{ type: 'COT_UID', value: 'U-2' }] }));
    fixture.detectChanges();
    open('origin');
    r = rows();
    expect(r['TAK uid']).toBe('U-2');
    expect(r['Callsign']).toBe('ALPHA 1');

    fixture.componentRef.setInput('entity', makeEntity({ id: 'e3', identities: [{ type: 'ICAO24', value: 'abc123' }] }));
    fixture.detectChanges();
    open('origin');
    r = rows();
    expect(r['TAK uid']).toBeUndefined();
    expect(r['Callsign']).toBeUndefined();
    httpMock.expectNone('/api/v1/feeds');
  });

  describe('header and key facts', () => {
    it('shows how fresh the last fix is, next to the type and affiliation', () => {
      fixture.componentRef.setInput('entity', makeEntity({ affiliation: 'HOSTILE' as Entity['affiliation'], lastSeenAt: secondsAgo(12) }));
      fixture.detectChanges();
      const pill = el.querySelector('.freshness')!;
      expect(pill.classList).toContain('fresh');
      expect(pill.textContent?.trim()).toBe('12 s ago');
      const chips = Array.from(el.querySelectorAll('.header-chips .chip')).map((c) => c.textContent?.trim());
      expect(chips).toEqual(['UNIT', 'HOSTILE']);
    });

    it('badges exercise data in the header chips, and nothing on a live or old entity', () => {
      fixture.componentRef.setInput('entity', makeEntity({ exercise: true }));
      fixture.detectChanges();
      expect(el.querySelector('.header-chips .chip-exercise')?.textContent?.trim()).toBe('EXERCISE');

      fixture.componentRef.setInput('entity', makeEntity());
      fixture.detectChanges();
      expect(el.querySelector('.chip-exercise')).toBeNull();

      fixture.componentRef.setInput('entity', makeEntity({ exercise: false }));
      fixture.detectChanges();
      expect(el.querySelector('.chip-exercise')).toBeNull();
    });

    it('marks a track the feed declared stale even when its timestamp is recent', () => {
      fixture.componentRef.setInput('entity', makeEntity({ lastSeenAt: secondsAgo(5), ageoutState: 'STALE' }));
      fixture.detectChanges();
      expect(el.querySelector('.freshness')?.classList).toContain('stale');
    });

    it('puts classification in a banner under the header', () => {
      fixture.componentRef.setInput('entity', makeEntity({ classification: Classification.SECRET }));
      fixture.detectChanges();
      const banner = el.querySelector('.classification-banner')!;
      expect(banner.textContent?.trim()).toBe('SECRET');
      expect(banner.classList).toContain('SECRET');
    });

    it('leads with position, movement, last fix and altitude', () => {
      fixture.componentRef.setInput(
        'entity',
        makeEntity({
          position: { latitude: 25.276987, longitude: 55.296249, altitude: 0 },
          circularError: 15,
          speedKnots: 12.4,
          heading: 47,
          course: 45,
          lastSeenAt: '2026-10-17T14:32:08Z',
        }),
      );
      fixture.detectChanges();
      const fact = (name: string) =>
        Array.from(el.querySelectorAll(`.key-facts .fact[data-fact="${name}"] > *`))
          .map((part) => part.textContent!.replace(/\s+/g, ' ').trim())
          .join(' ');
      expect(fact('position')).toBe('Position 25.2770° N 55.2962° E CEP 15 m');
      expect(fact('movement')).toBe('Speed · Heading 12.4 kts 047° course 045°');
      expect(fact('last-fix')).toBe('Last fix 14:32:08Z COT');
      expect(fact('altitude')).toBe('Altitude 0 m');
    });

    it('writes a satellite altitude in kilometres', () => {
      fixture.componentRef.setInput('entity', makeEntity({ entityType: EntityType.SATELLITE, position: { latitude: 0, longitude: 0, altitude: 408_250 } }));
      fixture.detectChanges();
      expect(el.querySelector('.key-facts .fact[data-fact="altitude"] .fact-value')?.textContent?.trim()).toBe('408 km');
    });

    it('shows a dash where a fact is unknown', () => {
      fixture.componentRef.setInput('entity', makeEntity());
      fixture.detectChanges();
      expect(el.querySelector('.key-facts .fact[data-fact="movement"] .fact-value')?.textContent?.trim()).toBe('—');
      expect(el.querySelector('.key-facts .fact[data-fact="position"] .fact-value')?.textContent?.trim()).toBe('—');
    });

    it('loads the 3D model only when its section is opened', () => {
      fixture.componentRef.setInput('entity', makeEntity({ entityType: EntityType.VESSEL }));
      fixture.detectChanges();
      expect(el.querySelector('iframe')).toBeNull();
      expect(el.querySelector('.section-toggle[data-section="model"] .section-summary')?.textContent?.trim()).toBe('vessel.glb');
      open('model');
      expect(el.querySelector('iframe')).not.toBeNull();
      // a unit has no model and no model section
      fixture.componentRef.setInput('entity', makeEntity({ entityType: EntityType.UNIT }));
      fixture.detectChanges();
      expect(el.querySelector('.section-toggle[data-section="model"]')).toBeNull();
    });

    it('opens identity by default and folds everything else', () => {
      fixture.componentRef.setInput('entity', makeEntity({ platformData: { ais: { mmsi: '352598000' } } as Entity['platformData'] }));
      fixture.detectChanges();
      const states = Array.from(el.querySelectorAll('.section-toggle')).map((t) => [t.getAttribute('data-section'), t.getAttribute('aria-expanded')]);
      expect(states).toContainEqual(['identity', 'true']);
      // A vessel's details (its voyage) open too, as a satellite's orbit does.
      expect(states).toContainEqual(['platformDetails', 'true']);
      expect(states.filter(([k]) => k !== 'identity' && k !== 'platformDetails').every(([, v]) => v === 'false')).toBe(true);
      expect(el.textContent).toContain('352598000');
    });
  });

  describe('behaviour section', () => {
    function makeBaseline(over: Partial<EntityBaseline> = {}): EntityBaseline {
      const hours = new Array(168).fill(0);
      hours[1 * 24 + 8] = 40;
      hours[1 * 24 + 9] = 20;
      return {
        entityId: 'e1',
        entityType: 'VEHICLE',
        computedAt: '2026-10-17T12:00:00Z',
        windowStart: '2026-09-17T12:00:00Z',
        windowEnd: '2026-10-17T12:00:00Z',
        pointCount: 3360,
        days: 10,
        mature: true,
        stats: {
          range: { p50M: 0, p95M: 3400 },
          speed: { mean: 4, stddev: 8, p95: 21, max: 22, n: 3360 },
          altitude: { mean: 10, stddev: 0, p95: 10, max: 10, n: 3360 },
          gap: { p50Sec: 300, p95Sec: 300 },
          params: { solarOffsetHours: -5, windowDays: 30 },
        },
        hours,
        places: [
          { clusterIndex: 0, name: 'Depot', lat: 40, lon: -74, radiusM: 40, visits: 11, medianDwellSec: 6 * 3600, pointShare: 0.6, firstSeen: '2026-10-07T00:00:00Z', lastSeen: '2026-10-17T00:00:00Z' },
          { clusterIndex: 1, lat: 40.05, lon: -74.05, radiusM: 35, visits: 10, medianDwellSec: 8.5 * 3600, pointShare: 0.3, firstSeen: '2026-10-07T00:00:00Z', lastSeen: '2026-10-17T00:00:00Z' },
        ],
        ...over,
      };
    }

    function makeFinding(over: Partial<Finding> = {}): Finding {
      return {
        id: 'f1', entityId: 'e1', kind: 'UNUSUAL_PLACE', severity: 'MEDIUM', score: 2, summary: '3.3 km from Depot',
        expected: {}, actual: {}, thresholds: {}, evidenceStart: '2026-10-17T12:00:00Z', evidenceEnd: '2026-10-17T12:10:00Z',
        status: 'OPEN', classification: 'UNCLASSIFIED', hits: 3, firstAt: '2026-10-17T12:00:00Z', lastAt: '2026-10-17T12:10:00Z',
        ...over,
      };
    }

    it('says so when there is no baseline yet', () => {
      fixture.componentRef.setInput('entity', makeEntity());
      fixture.detectChanges();
      expect(analytics.getBaseline).toHaveBeenCalledWith('e1');
      expect(el.querySelector('.section-toggle[data-section="behaviour"] .section-summary')?.textContent?.trim()).toBe('No baseline yet');
      open('behaviour');
      expect(el.querySelector('.behaviour-section .behaviour-note')?.textContent).toContain('No baseline yet');
      expect(el.querySelector('.place-list')).toBeNull();
    });

    it('keeps the behaviour section folded behind a one-line summary', () => {
      analytics.baseline = makeBaseline();
      analytics.findings = [makeFinding()];
      fixture.componentRef.setInput('entity', makeEntity());
      fixture.detectChanges();
      const toggle = el.querySelector('.section-toggle[data-section="behaviour"]')!;
      expect(toggle.getAttribute('aria-expanded')).toBe('false');
      expect(toggle.querySelector('.section-summary')?.textContent?.trim()).toBe('10 d baseline · 1 open');
      expect(el.querySelector('.place-list')).toBeNull();
    });

    it('shows the baseline, named places, the hour grid and open findings', () => {
      analytics.baseline = makeBaseline();
      analytics.findings = [makeFinding()];
      fixture.componentRef.setInput('entity', makeEntity());
      fixture.detectChanges();
      // open findings sit above the folded sections, not inside behaviour
      const finding = el.querySelector('.open-findings .finding-item')!;
      expect(finding.textContent).toContain('Unusual place');
      expect(finding.textContent).toContain('3.3 km from Depot');
      // a viewer gets no review buttons
      expect(el.querySelector('.finding-actions')).toBeNull();

      open('behaviour');
      const section = el.querySelector('.behaviour-section')!;
      expect(section.textContent).toContain('3360 fixes');
      expect(section.textContent).toContain('10 d');
      expect(section.textContent).toContain('0 m / 3.4 km');
      const places = Array.from(section.querySelectorAll('.place-item .place-name')).map((n) => n.textContent?.trim());
      expect(places).toEqual(['Depot', 'Place 2']);
      expect(section.querySelectorAll('.hour-cell').length).toBe(168);
      (section.querySelector('.place-item') as HTMLElement).click();
      expect(locations.flyTo).toHaveBeenCalledWith(expect.objectContaining({ name: 'Depot', latitude: 40, longitude: -74 }));
    });

    it('lets an analyst dismiss a finding, which removes it from the list', () => {
      auth.hasAnyRole.mockReturnValue(true);
      analytics.baseline = makeBaseline();
      analytics.findings = [makeFinding()];
      fixture.componentRef.setInput('entity', makeEntity());
      fixture.detectChanges();
      const dismiss = Array.from(el.querySelectorAll('.finding-actions .btn')).find((b) => b.textContent?.includes('Dismiss')) as HTMLButtonElement;
      dismiss.click();
      fixture.detectChanges();
      expect(analytics.reviewFinding).toHaveBeenCalledWith('f1', 'DISMISSED');
      expect(el.querySelector('.finding-item')).toBeNull();
    });
  });

  describe('satellite orbit section', () => {
    const line1 = '1 25544U 98067A   26063.86671769  .00009014  00000+0  17477-3 0  9999';
    const line2 = '2 25544  51.6315  96.2009 0008177 157.5272 202.6076 15.48447334555585';

    it('shows the overlay toggles, the epoch and the elements for a satellite, opened by default', () => {
      fixture.componentRef.setInput(
        'entity',
        makeEntity({
          entityType: EntityType.SATELLITE,
          name: 'ISS (ZARYA)',
          platformData: { tle: { noradId: 25544, line1, line2, epoch: '2026-03-04T20:48:04.408416Z', inclination: 51.6315 } },
        }),
      );
      fixture.detectChanges();
      const boxes = Array.from(el.querySelectorAll('.orbit-toggle input')) as HTMLInputElement[];
      expect(boxes.length).toBe(3);
      expect(boxes.every((b) => b.checked)).toBe(true);
      const orbit = TestBed.inject(OrbitOverlayService);
      boxes[1].click();
      fixture.detectChanges();
      expect(orbit.showGroundTrack()).toBe(false);
      expect(boxes[1].checked).toBe(false);
      orbit.toggle('groundTrack'); // restore for other tests
      expect(el.querySelector('.tle-lines')?.textContent).toContain(line2);
      expect(el.textContent).toContain('TLE Epoch');
    });

    it('shows no orbit controls for an entity without elements', () => {
      fixture.componentRef.setInput('entity', makeEntity());
      fixture.detectChanges();
      expect(el.querySelector('.orbit-toggle')).toBeNull();
      expect(el.querySelector('.tle-lines')).toBeNull();
    });
  });

  describe('vessel details section', () => {
    const vessel = (ais: Record<string, unknown>) =>
      makeEntity({ entityType: EntityType.VESSEL, name: 'PONTUS HIGHWAY', platformData: { ais: { mmsi: '352004564', ...ais } } as any });
    const rows = () =>
      Array.from(el.querySelectorAll('.field-row')).map((r) => `${r.querySelector('.field-label')?.textContent?.trim()} ${r.querySelector('.field-value')?.textContent?.trim()}`);

    it('opens with the category, status in words and a readable ETA', () => {
      fixture.componentRef.setInput(
        'entity',
        vessel({ shipType: 70, shipTypeName: 'Cargo', navStatus: 'UNDER_WAY_USING_ENGINE', destination: 'GBSOU', eta: '10-17T22:00' }),
      );
      fixture.detectChanges();
      const section = el.querySelector('[data-section="platformDetails"]') as HTMLElement;
      expect(section.getAttribute('aria-expanded')).toBe('true');
      expect(rows()).toContain('Ship Type Cargo');
      expect(rows()).toContain('Nav Status Under way (engine)');
      expect(rows()).toContain('Destination GBSOU');
      expect(rows()).toContain('ETA 17 Oct 22:00 UTC');
      expect((el.querySelector('.category-dot') as HTMLElement).style.background).toBe('rgb(34, 197, 94)');
    });

    it('names the category and the specific type when they differ, and hides an unknown status', () => {
      fixture.componentRef.setInput('entity', vessel({ shipType: 52, shipTypeName: 'Tug', navStatus: 'UNKNOWN' }));
      fixture.detectChanges();
      expect(rows()).toContain('Ship Type Service / special · Tug');
      expect(rows().some((r) => r?.startsWith('Nav Status'))).toBe(false);
    });

    it('summarises the voyage when folded', () => {
      fixture.componentRef.setInput('entity', vessel({ shipType: 80, shipTypeName: 'Tanker', navStatus: 'MOORED', destination: 'ROTTERDAM' }));
      fixture.detectChanges();
      (el.querySelector('[data-section="platformDetails"]') as HTMLElement).click();
      fixture.detectChanges();
      expect(el.querySelector('[data-section="platformDetails"] .section-summary')?.textContent?.trim()).toBe('Tanker · Moored · to ROTTERDAM');
    });
  });

  describe('flight path control', () => {
    const military = () =>
      makeEntity({ entityType: EntityType.AIRCRAFT, source: EntitySource.INTELSKY, name: 'CNV4062', platformData: { adsb: { icaoHex: 'AE7457', operatorName: 'United States Air Force' } } as any });

    it("opens a military aircraft's details with today's path and lets the range change", () => {
      const entity = military();
      const flightPath = TestBed.inject(FlightPathService);
      flightPath.select(entity); // the map does this on selection
      fixture.componentRef.setInput('entity', entity);
      fixture.detectChanges();

      expect(el.querySelector('[data-section="platformDetails"]')?.getAttribute('aria-expanded')).toBe('true');
      const buttons = Array.from(el.querySelectorAll('.path-range-btn')) as HTMLButtonElement[];
      expect(buttons.map((b) => b.textContent?.trim())).toEqual(['Off', 'Today', '3 d', '7 d']);
      expect(buttons[1].classList).toContain('active');
      expect(el.querySelector('.path-note')?.textContent).toContain('Loading');

      httpMock.expectOne('/api/v1/aircraft/AE7457/path?days=1').flush({ hex: 'AE7457', source: 'INTELSKY', days: 1, points: [{ lat: 1, lon: 2, altFt: 0, ts: 1792100404 }] });
      fixture.detectChanges();
      expect(el.querySelector('.path-note')?.textContent?.trim()).toBe('1 point since 15 Oct 21:40 UTC');

      buttons[2].click();
      fixture.detectChanges();
      httpMock.expectOne('/api/v1/aircraft/AE7457/path?days=3').flush({ hex: 'AE7457', source: 'INTELSKY', days: 3, points: [] });
      fixture.detectChanges();
      expect(el.querySelector('.path-note')?.textContent?.trim()).toBe('No IntelSky history for this aircraft');

      buttons[0].click();
      fixture.detectChanges();
      expect(el.querySelector('.path-note')).toBeNull();
      flightPath.select(null);
    });

    it('shows no control for an aircraft without an ICAO address', () => {
      fixture.componentRef.setInput('entity', makeEntity({ entityType: EntityType.AIRCRAFT, platformData: { adsb: {} } as any }));
      fixture.detectChanges();
      expect(el.querySelector('.path-range')).toBeNull();
    });
  });

  describe('RF section', () => {
    const signalEntity = (rf: Record<string, unknown>) =>
      makeEntity({ entityType: EntityType.SIGNAL, name: 'EMITTER-1', platformData: { rf } as any });
    const rfRows = () =>
      Array.from(el.querySelectorAll('.field-row')).map((r) => `${r.querySelector('.field-label')?.textContent?.trim()}: ${r.querySelector('.field-value')?.textContent?.trim()}`);

    it('opens by default when platformData.rf is present and formats frequency, bandwidth, PRI/PRF, scan and power', () => {
      fixture.componentRef.setInput(
        'entity',
        signalEntity({
          frequencyHz: 9_410_000_000,
          bandwidthHz: 25_000_000,
          emitterType: 'RADAR_SEARCH',
          modulation: 'PULSE',
          priUs: 400,
          pulseWidthUs: 1.2,
          scanType: 'CIRCULAR',
          scanRateRpm: 12,
          powerDbm: -62,
        }),
      );
      fixture.detectChanges();

      const section = el.querySelector('[data-section="platformDetails"]') as HTMLElement;
      expect(section.getAttribute('aria-expanded')).toBe('true');
      expect(section.querySelector('.section-label')?.textContent?.trim()).toBe('RF');

      const r = rfRows();
      expect(r).toContain('Frequency: 9.41 GHz');
      expect(r).toContain('Bandwidth: 25 MHz');
      expect(r).toContain('Emitter Type: RADAR_SEARCH');
      expect(r).toContain('Modulation: PULSE');
      expect(r).toContain('PRI / PRF: 400 µs (2.5 kHz)');
      expect(r).toContain('Pulse Width: 1.2 µs');
      expect(r).toContain('Scan: CIRCULAR · 12 rpm');
      expect(r).toContain('Power: -62 dBm');

      // When collapsed, summary shows frequency and emitter type
      section.click();
      fixture.detectChanges();
      expect(section.querySelector('.section-summary')?.textContent?.trim()).toBe('9.41 GHz · RADAR_SEARCH');
    });

    it('omits absent or zero RF fields except zero powerDbm', () => {
      fixture.componentRef.setInput(
        'entity',
        signalEntity({
          frequencyHz: 450,
          bandwidthHz: 0,
          priUs: 0,
          pulseWidthUs: 0,
          scanType: 'SECTOR',
          scanRateRpm: 0,
          powerDbm: 0,
        }),
      );
      fixture.detectChanges();

      const r = rfRows();
      expect(r).toContain('Frequency: 450 Hz');
      expect(r).toContain('Scan: SECTOR');
      expect(r).toContain('Power: 0 dBm');
      expect(r.some((row) => row.startsWith('Bandwidth:'))).toBe(false);
      expect(r.some((row) => row.startsWith('PRI / PRF:'))).toBe(false);
      expect(r.some((row) => row.startsWith('Pulse Width:'))).toBe(false);
    });
  });

  describe('Radar section', () => {
    const radarEntity = (radar: Record<string, unknown>, over: Partial<Entity> = {}) =>
      makeEntity({ entityType: EntityType.AIRCRAFT, name: 'TRACK 7', platformData: { radar } as any, ...over });
    const sectionRows = () =>
      Array.from(el.querySelectorAll('[data-section="platformDetails"] ~ .section-content .field-row')).map(
        (r) => `${r.querySelector('.field-label')?.textContent?.trim()}: ${r.querySelector('.field-value')?.textContent?.replace(/\s+/g, ' ').trim()}`,
      );

    it('shows each radar row and summarises the track when collapsed', () => {
      fixture.componentRef.setInput(
        'entity',
        radarEntity({
          rangeRateMps: -12.5,
          rcsDbsm: -18.5,
          snrDb: 21,
          mode: 'TRACK',
          trackQuality: 0.85,
          classification: 'DRONE',
          classificationConfidence: 0.92,
          radarTrackId: 'R-0042',
          heightAboveGroundM: 120,
        }),
      );
      fixture.detectChanges();

      const toggle = el.querySelector('[data-section="platformDetails"]') as HTMLElement;
      expect(toggle.getAttribute('aria-expanded')).toBe('true');
      expect(toggle.querySelector('.section-label')?.textContent?.trim()).toBe('Radar Track');
      const r = sectionRows();
      expect(r).toContain('Range rate: -12.5 m/s (approaching)');
      expect(r).toContain('RCS: -18.5 dBsm');
      expect(r).toContain('SNR: 21 dB');
      expect(r).toContain('Mode: TRACK');
      expect(r).toContain('Track quality: 85 %');
      expect(r).toContain('Classification: DRONE (92 %)');
      expect(r).toContain('Radar track: R-0042');
      expect(r).toContain('Height above ground: 120 m');

      toggle.click();
      fixture.detectChanges();
      expect(toggle.querySelector('.section-summary')?.textContent?.trim()).toBe('DRONE · TRACK');
    });

    it('says nothing about direction for a receding or zero range rate, and omits absent rows', () => {
      fixture.componentRef.setInput('entity', radarEntity({ rangeRateMps: 4, mode: 'SEARCH' }));
      fixture.detectChanges();
      const r = sectionRows();
      expect(r).toContain('Range rate: 4 m/s');
      expect(r).toContain('Mode: SEARCH');
      expect(r.some((row) => row.startsWith('RCS:') || row.startsWith('SNR:') || row.startsWith('Classification:'))).toBe(false);
      expect(r.some((row) => row.startsWith('Height above ground:'))).toBe(false);

      fixture.componentRef.setInput('entity', radarEntity({ rangeRateMps: 0, classificationConfidence: 0.5, classification: 'VEHICLE' }));
      fixture.detectChanges();
      expect(sectionRows()).toContain('Range rate: 0 m/s');
      expect(sectionRows()).toContain('Classification: VEHICLE (50 %)');
    });
  });

  describe('EO/IR section', () => {
    const eoirEntity = (eoir: Record<string, unknown>, over: Partial<Entity> = {}) =>
      makeEntity({ entityType: EntityType.AIRCRAFT, name: 'CAM 3', platformData: { eoir } as any, ...over });
    const sectionRows = () =>
      Array.from(el.querySelectorAll('[data-section="platformDetails"] ~ .section-content .field-row')).map(
        (r) => `${r.querySelector('.field-label')?.textContent?.trim()}: ${r.querySelector('.field-value')?.textContent?.replace(/\s+/g, ' ').trim()}`,
      );

    it('shows each EO/IR row and summarises the detection when collapsed', () => {
      fixture.componentRef.setInput(
        'entity',
        eoirEntity({ band: 'LWIR', classification: 'DRONE', classificationConfidence: 0.8, horizontalFovDeg: 12.5, verticalFovDeg: 7, cameraTrackId: 'C-9' }),
      );
      fixture.detectChanges();

      const toggle = el.querySelector('[data-section="platformDetails"]') as HTMLElement;
      expect(toggle.querySelector('.section-label')?.textContent?.trim()).toBe('EO/IR Detection');
      const r = sectionRows();
      expect(r).toContain('Band: LWIR');
      expect(r).toContain('Classification: DRONE (80 %)');
      expect(r).toContain('Field of view: 12.5° × 7°');
      expect(r).toContain('Camera track: C-9');
      expect(r.some((row) => row.startsWith('Placement:'))).toBe(false);

      toggle.click();
      fixture.detectChanges();
      expect(toggle.querySelector('.section-summary')?.textContent?.trim()).toBe('LWIR · DRONE');
    });

    it('notes a placement from a bearing, with its CEP95, only for a covariance and no radar detail', () => {
      const cov = { kinematics: { positionCovariance: { pnPn: 900, pePe: 900 } }, circularError: 73.4 };
      fixture.componentRef.setInput('entity', eoirEntity({ band: 'VISIBLE' }, cov));
      fixture.detectChanges();
      let r = sectionRows();
      expect(r).toContain('Placement: Placed on terrain from a bearing');
      expect(r).toContain('CEP95: 73 m');

      // a covariance with a radar detail beside it is the radar's, not a bearing's
      fixture.componentRef.setInput(
        'entity',
        makeEntity({ platformData: { eoir: { band: 'VISIBLE' }, radar: { mode: 'TRACK' } } as any, ...cov }),
      );
      fixture.detectChanges();
      expect(sectionRows().some((row) => row.startsWith('Placement:'))).toBe(false);

      // no covariance: no note
      fixture.componentRef.setInput('entity', eoirEntity({ band: 'VISIBLE' }));
      fixture.detectChanges();
      r = sectionRows();
      expect(r.some((row) => row.startsWith('Placement:') || row.startsWith('CEP95:'))).toBe(false);
    });
  });

  describe('Site bearings', () => {
    const bearing = (over: Record<string, unknown>) => ({
      trackId: 'T1',
      azimuthDeg: 123.4,
      elevationDeg: 5.25,
      lengthM: 5000,
      reason: 'air_target',
      observedAt: secondsAgo(3),
      ...over,
    });
    const site = (bearings: unknown[], fields: Record<string, string> = {}) =>
      makeEntity({
        entityType: EntityType.SENSOR,
        feedId: 'grpc-1',
        identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }],
        platformData: {
          generic: { fields: { heartbeat: 'online', heartbeatIntervalSec: '5', lastHeartbeatAt: secondsAgo(2), bearings: JSON.stringify(bearings), ...fields } },
        },
      });
    const bearingRows = () =>
      Array.from(el.querySelectorAll('.origin-section .bearing-row')).map((r) => (r.textContent ?? '').replace(/\s+/g, ' ').trim());

    function show(entity: Entity): void {
      fixture.componentRef.setInput('entity', entity);
      fixture.detectChanges();
      open('origin');
      httpMock.expectOne('/api/v1/feeds').flush([]);
      fixture.detectChanges();
    }

    it('lists each live bearing with its class, direction, age and reason', () => {
      show(
        site([
          bearing({ classification: 'DRONE', confidence: 0.91, band: 'LWIR' }),
          bearing({ trackId: 'T2', azimuthDeg: 270, elevationDeg: -1, reason: 'no_ground_intersection', observedAt: secondsAgo(7) }),
        ]),
      );
      const r = bearingRows();
      expect(r).toHaveLength(2);
      expect(r[0]).toContain('DRONE');
      expect(r[0]).toContain('91 %');
      expect(r[0]).toContain('az 123.4°');
      expect(r[0]).toContain('el 5.3°');
      expect(r[0]).toContain('3 s ago');
      expect(r[0]).toContain('Airborne, distance unknown');
      expect(r[1]).toContain('az 270°');
      expect(r[1]).toContain('el -1°');
      expect(r[1]).toContain('7 s ago');
      expect(r[1]).toContain('Above the horizon');
      expect(el.querySelector('.origin-section .bearing-list')?.closest('.field-row')?.querySelector('.field-label')?.textContent?.trim()).toBe('Bearings');
    });

    it('words every reason', () => {
      show(
        site([
          bearing({ reason: 'air_target' }),
          bearing({ reason: 'no_ground_intersection' }),
          bearing({ reason: 'terrain_unavailable' }),
          bearing({ reason: 'no_origin_altitude' }),
        ]),
      );
      const text = bearingRows().join('|');
      expect(text).toContain('Airborne, distance unknown');
      expect(text).toContain('Above the horizon');
      expect(text).toContain('Terrain unavailable');
      expect(text).toContain('Camera height unknown');
    });

    it('hides a bearing past its time to live and the whole list when none is left', () => {
      show(site([bearing({}), bearing({ trackId: 'T2', observedAt: secondsAgo(30) })]));
      expect(bearingRows()).toHaveLength(1);

      fixture.componentRef.setInput('entity', site([bearing({ observedAt: secondsAgo(30) })]));
      fixture.detectChanges();
      expect(bearingRows()).toHaveLength(0);
      expect(el.querySelector('.origin-section .bearing-list')).toBeNull();
    });

    it('opens the bearings and highlights the one clicked on the map', () => {
      fixture.componentRef.setInput('entity', site([bearing({}), bearing({ trackId: 'T2', azimuthDeg: 270 })]));
      fixture.componentRef.setInput('highlightBearing', 't:T2');
      fixture.detectChanges();
      httpMock.match('/api/v1/feeds').forEach((r) => r.flush([]));
      fixture.detectChanges();
      const rowsEl = Array.from(el.querySelectorAll('.origin-section .bearing-row'));
      expect(rowsEl).toHaveLength(2); // the section opened by itself
      expect(rowsEl.map((r) => r.classList.contains('highlighted'))).toEqual([false, true]);

      fixture.componentRef.setInput('highlightBearing', null);
      fixture.detectChanges();
      expect(el.querySelectorAll('.origin-section .bearing-row.highlighted')).toHaveLength(0);
    });

    it('keeps a bearing the feed gave a longer time to live', () => {
      show(site([bearing({ observedAt: secondsAgo(30) })], { bearingTtlSec: '60' }));
      expect(bearingRows()).toHaveLength(1);
    });
  });

  it("shows a site's heartbeat and, for an operator, links to its connection", () => {
    auth.hasAnyRole.mockImplementation((...roles: string[]) => roles.includes('sentinel-operator'));
    const navigate = jest.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    fixture.componentRef.setInput(
      'entity',
      makeEntity({
        entityType: EntityType.SENSOR,
        feedId: 'grpc-1',
        identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }],
        platformData: { generic: { fields: { heartbeat: 'online', heartbeatIntervalSec: '5', lastHeartbeatAt: secondsAgo(2) } } },
      }),
    );
    fixture.detectChanges();
    open('origin');
    httpMock.expectOne('/api/v1/feeds').flush([]);
    fixture.detectChanges();
    expect(rows()['Heartbeat']).toBe('online · every 5 s');
    (el.querySelector('.origin-section .field-link') as HTMLButtonElement).click();
    expect(navigate).toHaveBeenCalledWith(['/connections'], { queryParams: { open: 'grpc-1' } });
  });

  it("links a rebound site to its emplacement row's connection, not the entity's first feed", () => {
    auth.hasAnyRole.mockImplementation((...roles: string[]) => roles.includes('sentinel-operator'));
    const navigate = jest.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    TestBed.inject(EmplacementService).emplacements.set([
      {
        id: 'emp-1', name: 'Mast', description: '', mobility: 'FIXED', sensorType: 'RADAR',
        position: { lat: 50, lon: -4 }, altitude: null, boresightDeg: 0, azimuthReference: 'TRUE_NORTH',
        coverage: { minRangeM: 0, maxRangeM: 60000, azimuthWidthDeg: 360 }, defaultSigma: {},
        feedId: 'grpc-2', classification: 'UNCLASSIFIED', exercise: false, active: true,
        lastReportAt: null, createdBy: 'u', createdAt: '', updatedAt: '', version: 2,
      },
    ]);
    fixture.componentRef.setInput(
      'entity',
      makeEntity({
        entityType: EntityType.SENSOR,
        feedId: 'grpc-1',
        identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }],
        platformData: { generic: { fields: { heartbeat: 'online', heartbeatIntervalSec: '5', lastHeartbeatAt: secondsAgo(2) } } },
      }),
    );
    fixture.detectChanges();
    open('origin');
    httpMock.expectOne('/api/v1/feeds').flush([]);
    fixture.detectChanges();
    const link = el.querySelector('.origin-section .field-link') as HTMLButtonElement;
    expect(link.textContent?.trim()).toBe('Mast');
    expect(link.getAttribute('title')).toBe('Open its connection, grpc-2');
    link.click();
    expect(navigate).toHaveBeenCalledWith(['/connections'], { queryParams: { open: 'grpc-2' } });
  });

  it("shows a silent site's heartbeat with since when, and no link for a viewer", () => {
    fixture.componentRef.setInput(
      'entity',
      makeEntity({
        entityType: EntityType.SENSOR,
        feedId: 'grpc-1',
        identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }],
        platformData: { generic: { fields: { heartbeat: 'offline', heartbeatIntervalSec: '5', lastHeartbeatAt: '2026-10-17T14:02:00Z', silentSince: '2026-10-17T14:02:00Z' } } },
      }),
    );
    fixture.detectChanges();
    open('origin');
    httpMock.expectOne('/api/v1/feeds').flush([]);
    fixture.detectChanges();
    expect(rows()['Heartbeat']).toBe('offline since 14:02Z · every 5 s');
    expect(el.querySelector('.origin-section .field-link')).toBeNull();
  });

  describe('when newer data for the shown entity arrives', () => {
    const site = (heartbeatAgoSec: number, id = 'site-1') =>
      makeEntity({
        id,
        entityType: EntityType.SENSOR,
        feedId: 'grpc-1',
        identities: [{ type: 'EMPLACEMENT', value: 'emp-1' }],
        lastSeenAt: secondsAgo(heartbeatAgoSec),
        platformData: { generic: { fields: { heartbeat: 'online', heartbeatIntervalSec: '5', lastHeartbeatAt: secondsAgo(heartbeatAgoSec) } } },
      });
    beforeEach(() => {
      analytics.getBaseline.mockClear();
      analytics.getEntityFindings.mockClear();
    });
    const isOpen = (section: string) =>
      el.querySelector(`.section-toggle[data-section="${section}"]`)?.getAttribute('aria-expanded') === 'true';

    it('moves a site from offline to online when a fresh heartbeat arrives for the same id', () => {
      fixture.componentRef.setInput('entity', site(60));
      fixture.detectChanges();
      open('origin');
      httpMock.expectOne('/api/v1/feeds').flush([]);
      fixture.detectChanges();
      expect(rows()['Heartbeat']).toMatch(/^offline since/);

      fixture.componentRef.setInput('entity', site(2));
      fixture.detectChanges();

      expect(rows()['Heartbeat']).toBe('online · every 5 s');
    });

    it('keeps the sections the operator opened and does not ask for the behaviour again', () => {
      fixture.componentRef.setInput('entity', site(2));
      fixture.detectChanges();
      httpMock.match('/api/v1/feeds').forEach((r) => r.flush([]));
      open('origin');
      expect(isOpen('origin')).toBe(true);
      expect(analytics.getBaseline).toHaveBeenCalledTimes(1);
      expect(analytics.getEntityFindings).toHaveBeenCalledTimes(1);

      fixture.componentRef.setInput('entity', site(1));
      fixture.detectChanges();

      expect(isOpen('origin')).toBe(true);
      expect(analytics.getBaseline).toHaveBeenCalledTimes(1);
      expect(analytics.getEntityFindings).toHaveBeenCalledTimes(1);
    });

    it('still resets the sections and asks again when a different entity is shown', () => {
      fixture.componentRef.setInput('entity', site(2));
      fixture.detectChanges();
      httpMock.match('/api/v1/feeds').forEach((r) => r.flush([]));
      open('origin');
      expect(isOpen('origin')).toBe(true);

      fixture.componentRef.setInput('entity', site(2, 'site-2'));
      fixture.detectChanges();

      expect(isOpen('origin')).toBe(false);
      expect(analytics.getBaseline).toHaveBeenCalledTimes(2);
      expect(analytics.getBaseline).toHaveBeenLastCalledWith('site-2');
      expect(analytics.getEntityFindings).toHaveBeenCalledTimes(2);
    });
  });
});
