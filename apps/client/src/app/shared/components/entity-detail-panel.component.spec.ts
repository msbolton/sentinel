import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { signal } from '@angular/core';
import { of } from 'rxjs';
import { EntityDetailPanelComponent } from './entity-detail-panel.component';
import { AnalyticsService } from '../../core/services/analytics.service';
import { AuthService } from '../../core/services/auth.service';
import { LocationService } from '../../core/services/location.service';
import { EntityBaseline, Finding } from '../models/finding.model';
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

    fixture.componentRef.setInput('entity', makeEntity({ identities: [{ type: 'COT_UID', value: 'U-2' }] }));
    fixture.detectChanges();
    open('origin');
    r = rows();
    expect(r['TAK uid']).toBe('U-2');
    expect(r['Callsign']).toBe('ALPHA 1');

    fixture.componentRef.setInput('entity', makeEntity({ identities: [{ type: 'ICAO24', value: 'abc123' }] }));
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
});
