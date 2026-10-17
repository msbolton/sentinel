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

describe('EntityDetailPanelComponent', () => {
  let fixture: ComponentFixture<EntityDetailPanelComponent>;
  let el: HTMLElement;
  let httpMock: HttpTestingController;

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

  afterEach(() => httpMock.verify());

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
    expect(el.querySelector('.chip-classification')?.textContent?.trim()).toBe('UNCLASSIFIED');
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

    let r = rows();
    expect(r['Connection']).toBeUndefined();
    expect(r['Callsign']).toBe('HAWK');
    expect(r['Contributing feeds']).toBeUndefined();

    fixture.componentRef.setInput('entity', makeEntity({ identities: [{ type: 'COT_UID', value: 'U-2' }] }));
    fixture.detectChanges();
    r = rows();
    expect(r['TAK uid']).toBe('U-2');
    expect(r['Callsign']).toBe('ALPHA 1');

    fixture.componentRef.setInput('entity', makeEntity({ identities: [{ type: 'ICAO24', value: 'abc123' }] }));
    fixture.detectChanges();
    r = rows();
    expect(r['TAK uid']).toBeUndefined();
    expect(r['Callsign']).toBeUndefined();
    httpMock.expectNone('/api/v1/feeds');
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
      expect(el.querySelector('.behaviour-section .behaviour-note')?.textContent).toContain('No baseline yet');
      expect(el.querySelector('.place-list')).toBeNull();
    });

    it('shows the baseline, named places, the hour grid and open findings', () => {
      analytics.baseline = makeBaseline();
      analytics.findings = [makeFinding()];
      fixture.componentRef.setInput('entity', makeEntity());
      fixture.detectChanges();
      const section = el.querySelector('.behaviour-section')!;
      expect(section.textContent).toContain('3360 fixes');
      expect(section.textContent).toContain('10 d');
      expect(section.textContent).toContain('0 m / 3.4 km');
      const places = Array.from(section.querySelectorAll('.place-item .place-name')).map((n) => n.textContent?.trim());
      expect(places).toEqual(['Depot', 'Place 2']);
      expect(section.querySelectorAll('.hour-cell').length).toBe(168);
      expect(section.querySelector('.finding-item')?.textContent).toContain('Unusual place');
      expect(section.querySelector('.finding-item')?.textContent).toContain('3.3 km from Depot');
      // a viewer gets no review buttons
      expect(section.querySelector('.finding-actions')).toBeNull();
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
});
