import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Subject, of, throwError } from 'rxjs';
import { AlertTickerComponent } from './alert-ticker.component';
import { AlertService } from '../../core/services/alert.service';
import { SelectionService } from '../../core/services/selection.service';
import { Alert, AlertSeverity, AlertType } from '../models/alert.model';

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
  let selection: { selectById: jest.Mock };

  async function create(): Promise<ComponentFixture<AlertTickerComponent>> {
    await TestBed.configureTestingModule({
      imports: [AlertTickerComponent],
      providers: [
        { provide: AlertService, useValue: { alertStream$: stream, getAlerts } },
        { provide: SelectionService, useValue: selection },
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
    selection = { selectById: jest.fn() };
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

    expect(getAlerts).toHaveBeenCalledWith({ acknowledged: false, limit: 5 });
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
