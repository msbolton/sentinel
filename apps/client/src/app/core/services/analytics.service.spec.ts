import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { AnalyticsService } from './analytics.service';
import { WebSocketService } from './websocket.service';
import { Alert } from '../../shared/models/alert.model';
import { Finding } from '../../shared/models/finding.model';

const finding: Finding = {
  id: 'f1',
  entityId: 'e1',
  kind: 'UNUSUAL_PLACE',
  severity: 'MEDIUM',
  score: 2.1,
  summary: '3.3 km from place 1',
  expected: { thresholdM: 2000 },
  actual: { distanceM: 3300 },
  thresholds: {},
  evidenceStart: '2026-10-17T12:00:00Z',
  evidenceEnd: '2026-10-17T12:05:00Z',
  status: 'OPEN',
  classification: 'UNCLASSIFIED',
  hits: 2,
  firstAt: '2026-10-17T12:00:00Z',
  lastAt: '2026-10-17T12:05:00Z',
};

describe('AnalyticsService', () => {
  let service: AnalyticsService;
  let http: HttpTestingController;
  let alerts: Subject<Alert>;

  beforeEach(() => {
    alerts = new Subject<Alert>();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: WebSocketService, useValue: { alertStream$: alerts.asObservable() } },
      ],
    });
    service = TestBed.inject(AnalyticsService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  function flushInitialCount(total = 3): void {
    const req = http.expectOne((r) => r.url === '/api/v1/analytics/findings' && r.params.get('status') === 'OPEN');
    req.flush({ data: [], total, page: 1, pageSize: 1 });
  }

  it('counts open findings on start', () => {
    expect(service.openCount()).toBeNull();
    flushInitialCount(3);
    expect(service.openCount()).toBe(3);
  });

  it('getFindings sends the filters as query parameters', () => {
    flushInitialCount();
    service.getFindings({ status: 'OPEN', kinds: ['UNUSUAL_PLACE', 'WENT_DARK'], severity: 'HIGH', entityId: 'e1', page: 2, pageSize: 25 }).subscribe();
    const req = http.expectOne((r) => r.url === '/api/v1/analytics/findings' && r.params.get('page') === '2');
    expect(req.request.params.get('kinds')).toBe('UNUSUAL_PLACE,WENT_DARK');
    expect(req.request.params.get('severity')).toBe('HIGH');
    expect(req.request.params.get('entityId')).toBe('e1');
    expect(req.request.params.get('pageSize')).toBe('25');
    req.flush({ data: [finding], total: 1, page: 2, pageSize: 25 });
  });

  it('getBaseline turns a 404 into null', () => {
    flushInitialCount();
    let result: unknown = 'unset';
    service.getBaseline('e1').subscribe((b) => (result = b));
    http.expectOne('/api/v1/analytics/entities/e1/baseline').flush({ message: 'none' }, { status: 404, statusText: 'Not Found' });
    expect(result).toBeNull();
  });

  it('reviewFinding patches the status and refreshes the count', () => {
    flushInitialCount(3);
    let reviewed: Finding | undefined;
    service.reviewFinding('f1', 'DISMISSED').subscribe((f) => (reviewed = f));
    const patch = http.expectOne('/api/v1/analytics/findings/f1');
    expect(patch.request.method).toBe('PATCH');
    expect(patch.request.body).toEqual({ status: 'DISMISSED' });
    patch.flush({ ...finding, status: 'DISMISSED' });
    expect(reviewed?.status).toBe('DISMISSED');
    flushInitialCount(2);
    expect(service.openCount()).toBe(2);
  });

  it('an alert that came from a finding refreshes the count', () => {
    flushInitialCount(1);
    alerts.next({ id: 'a1', metadata: { findingId: 'f1' } } as unknown as Alert);
    flushInitialCount(2);
    expect(service.openCount()).toBe(2);
    alerts.next({ id: 'a2', metadata: {} } as unknown as Alert);
    http.expectNone((r) => r.url === '/api/v1/analytics/findings');
  });
});
