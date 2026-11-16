import { readFileSync } from 'fs';
import { join } from 'path';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject, of, throwError } from 'rxjs';
import { AlertTickerComponent } from './alert-ticker.component';
import { AlertService } from '../../core/services/alert.service';
import { EntityService } from '../../core/services/entity.service';
import { SelectionService } from '../../core/services/selection.service';
import { WebSocketService } from '../../core/services/websocket.service';
import { Alert, AlertSeverity, AlertType } from '../models/alert.model';
import { Classification, Entity, EntitySource, EntityType } from '../models/entity.model';

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

describe('AlertTickerComponent', () => {
  let stream: Subject<Alert>;
  let getAlerts: jest.Mock;
  let selection: { selectById: jest.Mock; select: jest.Mock; flyTo: jest.Mock };
  let entities: { getEntity: jest.Mock };

  async function create(): Promise<ComponentFixture<AlertTickerComponent>> {
    await TestBed.configureTestingModule({
      imports: [AlertTickerComponent],
      providers: [
        { provide: AlertService, useValue: { alertStream$: stream, getAlerts } },
        { provide: SelectionService, useValue: selection },
        { provide: EntityService, useValue: entities },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(AlertTickerComponent);
    fixture.detectChanges();
    return fixture;
  }

  const ticks = (f: ComponentFixture<AlertTickerComponent>): HTMLButtonElement[] =>
    Array.from(f.nativeElement.querySelectorAll('.tick'));

  beforeEach(() => {
    stream = new Subject<Alert>();
    getAlerts = jest.fn(() => of({ data: [], total: 0 }));
    selection = { selectById: jest.fn(() => true), select: jest.fn(), flyTo: jest.fn() };
    entities = { getEntity: jest.fn() };
  });

  it('renders unacknowledged alerts newest first and selects on click', async () => {
    const fixture = await create();
    const alertA = alert('a', { title: 'Geofence breach', entityId: 'trk-a', createdAt: '2026-11-01T14:30:00Z' });
    const alertB = alert('b', { title: 'Speed anomaly', entityId: 'trk-b', entityName: 'MV Atlas', severity: AlertSeverity.CRITICAL });
    stream.next(alertA);
    stream.next(alertB);
    fixture.detectChanges();

    const t = ticks(fixture);
    expect(t).toHaveLength(2);
    expect(t[0].textContent).toContain('SPEED ANOMALY');
    expect(t[0].textContent).toContain('· MV ATLAS');
    expect(t[1].textContent).toContain('GEOFENCE BREACH');
    expect(t[1].textContent).not.toContain('·');

    t[0].click();
    expect(selection.selectById).toHaveBeenCalledWith('trk-b');
  });

  it('shows the time as HHMMZ and marks severity', async () => {
    const fixture = await create();
    stream.next(alert('h', { severity: AlertSeverity.HIGH, createdAt: '2026-11-01T04:05:59Z' }));
    stream.next(alert('w', { severity: AlertSeverity.MEDIUM }));
    stream.next(alert('l', { severity: AlertSeverity.LOW }));
    fixture.detectChanges();

    const [low, warn, hot] = ticks(fixture);
    expect(hot.textContent).toContain('0405Z ALERT H');
    expect(hot.classList.contains('h')).toBe(true);
    expect(warn.classList.contains('w')).toBe(true);
    expect(low.classList.contains('h') || low.classList.contains('w')).toBe(false);
    expect(hot.getAttribute('aria-label')).toContain('HIGH');
  });

  it('fetches and selects a track that is not cached', async () => {
    const fetched: Entity = {
      id: 'trk-far',
      entityType: EntityType.VESSEL,
      name: 'MV FAR',
      source: EntitySource.OPENSKY,
      classification: Classification.UNCLASSIFIED,
      metadata: {},
      affiliations: [],
      createdAt: '2026-11-01T00:00:00Z',
      updatedAt: '2026-11-01T00:00:00Z',
    };
    selection.selectById.mockReturnValue(false);
    entities.getEntity.mockReturnValue(of(fetched));
    const fixture = await create();
    stream.next(alert('far', { entityId: 'trk-far' }));
    fixture.detectChanges();

    ticks(fixture)[0].click();

    expect(selection.selectById).toHaveBeenCalledWith('trk-far');
    expect(entities.getEntity).toHaveBeenCalledWith('trk-far');
    expect(selection.select).toHaveBeenCalledWith(fetched);
    expect(selection.flyTo).not.toHaveBeenCalled();
  });

  it('flies to the alert position when the track cannot be fetched', async () => {
    selection.selectById.mockReturnValue(false);
    entities.getEntity.mockReturnValue(throwError(() => new Error('404')));
    const fixture = await create();
    stream.next(alert('gone', { entityId: 'trk-gone', position: { latitude: 12.5, longitude: 43.2 } }));
    fixture.detectChanges();

    ticks(fixture)[0].click();

    expect(selection.select).not.toHaveBeenCalled();
    expect(selection.flyTo).toHaveBeenCalledWith({ lat: 12.5, lon: 43.2 });
  });

  it('does not fetch a track it found in the cache', async () => {
    const fixture = await create();
    stream.next(alert('near', { entityId: 'trk-near' }));
    fixture.detectChanges();
    ticks(fixture)[0].click();
    expect(selection.selectById).toHaveBeenCalledWith('trk-near');
    expect(entities.getEntity).not.toHaveBeenCalled();
  });

  it('stays between the corner readouts and moves clear of an open route panel', () => {
    const source = readFileSync(join(__dirname, 'alert-ticker.component.ts'), 'utf8');
    expect(source).toMatch(/max-width: calc\(100vw - 600px\);\s*overflow: hidden;/);
    expect(source).toMatch(
      /:host\(\.panel-open\) \{\s*left: calc\(\(100vw - var\(--panel-width\)\) \/ 2\);\s*max-width: calc\(100vw - var\(--panel-width\) - 600px\);/,
    );
  });

  it('does nothing on click when the alert has no entity', async () => {
    const fixture = await create();
    stream.next(alert('x'));
    fixture.detectChanges();
    ticks(fixture)[0].click();
    expect(selection.selectById).not.toHaveBeenCalled();
  });

  it('seeds from unacknowledged alerts, newest first', async () => {
    getAlerts.mockReturnValue(
      of({
        data: [
          alert('old', { title: 'Older', createdAt: '2026-11-01T10:00:00Z' }),
          alert('new', { title: 'Newer', createdAt: '2026-11-01T12:00:00Z' }),
        ],
        total: 2,
      }),
    );
    const fixture = await create();

    expect(getAlerts).toHaveBeenCalledWith({ acknowledged: false, pageSize: 5 });
    const t = ticks(fixture);
    expect(t[0].textContent).toContain('NEWER');
    expect(t[1].textContent).toContain('OLDER');
  });

  it('drops a ticker alert when it is acknowledged', async () => {
    const fixture = await create();
    stream.next(alert('a'));
    stream.next(alert('b'));
    stream.next(alert('a', { acknowledged: true }));
    fixture.detectChanges();
    expect(ticks(fixture)).toHaveLength(1);
    expect(ticks(fixture)[0].textContent).toContain('ALERT B');
  });

  it('renders nothing when the list is empty, even if the initial fetch fails', async () => {
    getAlerts.mockReturnValue(throwError(() => new Error('offline')));
    const fixture = await create();
    expect(ticks(fixture)).toHaveLength(0);
    expect(fixture.nativeElement.children).toHaveLength(0);
  });

  it('keeps an alert that streamed in before the initial fetch returned', async () => {
    const page = new Subject<{ data: Alert[]; total: number }>();
    getAlerts.mockReturnValue(page);
    const fixture = await create();
    stream.next(alert('live', { title: 'Live' }));
    page.next({ data: [alert('live', { title: 'Live' }), alert('seed', { title: 'Seed' })], total: 2 });
    fixture.detectChanges();

    const t = ticks(fixture);
    expect(t).toHaveLength(2);
    expect(t[0].textContent).toContain('LIVE');
    expect(t[1].textContent).toContain('SEED');
  });

  it('unsubscribes from the stream on destroy', async () => {
    const fixture = await create();
    expect(stream.observed).toBe(true);
    fixture.destroy();
    expect(stream.observed).toBe(false);
  });
});

describe('AlertTickerComponent with the real AlertService', () => {
  it('drops a tick when the alert is acknowledged through the service', async () => {
    const ws = new Subject<Alert>();
    await TestBed.configureTestingModule({
      imports: [AlertTickerComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: WebSocketService, useValue: { alertStream$: ws } },
        { provide: SelectionService, useValue: { selectById: jest.fn() } },
        { provide: EntityService, useValue: { getEntity: jest.fn() } },
      ],
    }).compileComponents();
    const http = TestBed.inject(HttpTestingController);
    const fixture = TestBed.createComponent(AlertTickerComponent);
    fixture.detectChanges();
    for (const req of http.match((r) => r.url === '/api/v1/alerts')) req.flush({ data: [], total: 0 });

    ws.next(alert('a', { title: 'Geofence breach' }));
    ws.next(alert('b', { title: 'Speed anomaly' }));
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelectorAll('.tick')).toHaveLength(2);

    TestBed.inject(AlertService).acknowledgeAlert('a').subscribe();
    http.expectOne('/api/v1/alerts/a/acknowledge').flush(alert('a', { acknowledgedBy: 'w.operator' }));
    fixture.detectChanges();

    const left: HTMLElement[] = Array.from(fixture.nativeElement.querySelectorAll('.tick'));
    expect(left).toHaveLength(1);
    expect(left[0].textContent).toContain('SPEED ANOMALY');
    http.verify();
  });
});
