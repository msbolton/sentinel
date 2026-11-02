import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { AlertService } from './alert.service';
import { WebSocketService } from './websocket.service';
import { Alert, AlertSeverity, AlertType } from '../../shared/models/alert.model';

function alert(id: string, over: Partial<Alert> = {}): Alert {
  return {
    id,
    title: `Alert ${id}`,
    message: '',
    severity: AlertSeverity.MEDIUM,
    alertType: AlertType.UNSPECIFIED,
    acknowledged: false,
    metadata: {},
    createdAt: '2026-11-01T14:32:00Z',
    updatedAt: '2026-11-01T14:32:00Z',
    ...over,
  };
}

describe('AlertService', () => {
  let ws: Subject<Alert>;
  let service: AlertService;
  let http: HttpTestingController;

  beforeEach(() => {
    ws = new Subject<Alert>();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: WebSocketService, useValue: { alertStream$: ws } },
      ],
    });
    service = TestBed.inject(AlertService);
    http = TestBed.inject(HttpTestingController);
    http.expectOne((r) => r.url === '/api/v1/alerts').flush({ data: [], total: 0 });
  });

  afterEach(() => http.verify());

  it('streams the websocket alerts', () => {
    const seen: Alert[] = [];
    service.alertStream$.subscribe((a) => seen.push(a));
    ws.next(alert('a'));
    expect(seen.map((a) => a.id)).toEqual(['a']);
  });

  it('streams an alert as acknowledged once the acknowledge call succeeds', () => {
    const seen: Alert[] = [];
    service.alertStream$.subscribe((a) => seen.push(a));

    service.acknowledgeAlert('a').subscribe();
    expect(seen).toEqual([]);
    const req = http.expectOne('/api/v1/alerts/a/acknowledge');
    expect(req.request.method).toBe('PATCH');
    req.flush(alert('a', { acknowledgedBy: 'w.operator' }));

    expect(seen).toHaveLength(1);
    expect(seen[0]).toMatchObject({ id: 'a', acknowledged: true, acknowledgedBy: 'w.operator' });
  });

  it('still streams the acknowledgement when the response has no body', () => {
    const seen: Alert[] = [];
    service.alertStream$.subscribe((a) => seen.push(a));
    service.acknowledgeAlert('b').subscribe();
    http.expectOne('/api/v1/alerts/b/acknowledge').flush(null);
    expect(seen).toEqual([{ id: 'b', acknowledged: true }]);
  });

  it('streams nothing when the acknowledge call fails', () => {
    const seen: Alert[] = [];
    service.alertStream$.subscribe((a) => seen.push(a));
    service.acknowledgeAlert('c').subscribe({ error: () => undefined });
    http.expectOne('/api/v1/alerts/c/acknowledge').flush('no', { status: 500, statusText: 'Server Error' });
    expect(seen).toEqual([]);
  });
});
