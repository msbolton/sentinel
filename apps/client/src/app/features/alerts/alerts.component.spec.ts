import { ComponentFixture, TestBed } from '@angular/core/testing';
import { BehaviorSubject, Subject, of, throwError } from 'rxjs';
import { tap } from 'rxjs/operators';
import { AlertsComponent } from './alerts.component';
import { AlertService } from '../../core/services/alert.service';
import { EntityService } from '../../core/services/entity.service';
import { SelectionService } from '../../core/services/selection.service';
import { Alert, AlertSeverity, AlertType } from '../../shared/models/alert.model';

function alert(id: string, over: Partial<Alert> = {}): Alert {
  return {
    id,
    title: `Alert ${id}`,
    message: 'Something happened',
    severity: AlertSeverity.HIGH,
    alertType: AlertType.UNSPECIFIED,
    acknowledged: false,
    metadata: {},
    createdAt: '2026-11-01T14:32:00Z',
    updatedAt: '2026-11-01T14:32:00Z',
    ...over,
  };
}

describe('AlertsComponent', () => {
  let stream: Subject<Alert>;
  let acknowledgeAlert: jest.Mock;
  let selection: { selectById: jest.Mock; select: jest.Mock; flyTo: jest.Mock };
  let getEntity: jest.Mock;

  async function create(): Promise<ComponentFixture<AlertsComponent>> {
    await TestBed.configureTestingModule({
      imports: [AlertsComponent],
      providers: [
        {
          provide: AlertService,
          useValue: {
            alertStream$: stream,
            unacknowledgedCount$: new BehaviorSubject<number>(1),
            getAlerts: jest.fn(() =>
              of({ data: [alert('a', { title: 'Geofence breach' })], total: 1 }),
            ),
            acknowledgeAlert,
            refreshUnacknowledgedCount: jest.fn(),
          },
        },
        { provide: SelectionService, useValue: selection },
        { provide: EntityService, useValue: { getEntity } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(AlertsComponent);
    fixture.detectChanges();
    return fixture;
  }

  const cards = (f: ComponentFixture<AlertsComponent>): HTMLElement[] =>
    Array.from(f.nativeElement.querySelectorAll('.alert-card'));

  beforeEach(() => {
    stream = new Subject<Alert>();
    // A raw PATCH response: no `acknowledged` boolean and no title.
    acknowledgeAlert = jest.fn((id: string) =>
      of({ id, acknowledgedBy: 'w.operator' } as unknown as Alert),
    );
    selection = { selectById: jest.fn(() => false), select: jest.fn(), flyTo: jest.fn() };
    getEntity = jest.fn(() => throwError(() => new Error('gone')));
  });

  it('loads a page of unacknowledged alerts with the API\'s parameter names', async () => {
    const fixture = await create();
    const getAlerts = TestBed.inject(AlertService).getAlerts as jest.Mock;
    expect(getAlerts).toHaveBeenCalledWith({ severity: undefined, alertType: undefined, acknowledged: false, pageSize: 100 });
    expect(fixture.componentInstance.alerts()).toHaveLength(1);
    expect(TestBed.inject(AlertService).refreshUnacknowledgedCount).toHaveBeenCalled();
  });

  it('replaces a row in place when the stream updates an existing id', async () => {
    const fixture = await create();
    expect(cards(fixture)).toHaveLength(1);

    stream.next({ id: 'a', acknowledged: true } as unknown as Alert);
    fixture.detectChanges();

    expect(fixture.componentInstance.alerts()).toHaveLength(1);
    const rows = cards(fixture);
    expect(rows).toHaveLength(1);
    expect(rows[0].classList.contains('acknowledged')).toBe(true);
    expect(rows[0].textContent).toContain('Geofence breach');
  });

  it('adds a stream alert with a new id on top', async () => {
    const fixture = await create();
    stream.next(alert('b', { title: 'Speed anomaly' }));
    fixture.detectChanges();

    const rows = cards(fixture);
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain('Speed anomaly');
    expect(rows[1].textContent).toContain('Geofence breach');
  });

  it('keeps one acknowledged row with its title after clicking Acknowledge', async () => {
    // Mirror AlertService, which also feeds the stream from inside the PATCH map.
    acknowledgeAlert.mockImplementation((id: string) =>
      of({ id, acknowledgedBy: 'w.operator' } as unknown as Alert).pipe(
        tap((res) => stream.next({ ...res, id, acknowledged: true })),
      ),
    );
    const fixture = await create();

    const button: HTMLButtonElement = cards(fixture)[0].querySelector('.alert-actions button')!;
    button.click();
    fixture.detectChanges();

    expect(acknowledgeAlert).toHaveBeenCalledWith('a');
    const rows = cards(fixture);
    expect(rows).toHaveLength(1);
    expect(rows[0].classList.contains('acknowledged')).toBe(true);
    expect(rows[0].textContent).toContain('Geofence breach');
    expect(rows[0].textContent).toContain('Acknowledged');
    expect(rows[0].textContent).toContain('w.operator');
  });

  it('selects the alerted track from the map cache on click', async () => {
    selection.selectById.mockReturnValue(true);
    const fixture = await create();
    stream.next(alert('t', { entityId: 'e1', position: { latitude: 1, longitude: 2 } }));
    fixture.detectChanges();

    cards(fixture)[0].click();

    expect(selection.selectById).toHaveBeenCalledWith('e1');
    expect(getEntity).not.toHaveBeenCalled();
    expect(selection.flyTo).not.toHaveBeenCalled();
  });

  it('fetches the track when it is not loaded, and selects it', async () => {
    const entity = { id: 'e1', name: 'X' };
    getEntity.mockReturnValue(of(entity));
    const fixture = await create();
    stream.next(alert('t', { entityId: 'e1' }));
    fixture.detectChanges();

    cards(fixture)[0].click();

    expect(getEntity).toHaveBeenCalledWith('e1');
    expect(selection.select).toHaveBeenCalledWith(entity);
  });

  it('flies to where the alert fired when the track cannot be fetched', async () => {
    const fixture = await create();
    stream.next(alert('t', { entityId: 'e1', position: { latitude: 25.2, longitude: 55.3 } }));
    fixture.detectChanges();

    cards(fixture)[0].click();

    expect(selection.flyTo).toHaveBeenCalledWith({ lat: 25.2, lon: 55.3 });
  });

  it('flies to the position when the alert names no track', async () => {
    const fixture = await create();
    stream.next(alert('t', { position: { latitude: 3, longitude: 4 } }));
    fixture.detectChanges();

    cards(fixture)[0].click();

    expect(getEntity).not.toHaveBeenCalled();
    expect(selection.flyTo).toHaveBeenCalledWith({ lat: 3, lon: 4 });
  });

  it('reads as acknowledged when the PATCH response omits the boolean', async () => {
    const fixture = await create();

    const button: HTMLButtonElement = cards(fixture)[0].querySelector('.alert-actions button')!;
    button.click();
    fixture.detectChanges();

    const rows = cards(fixture);
    expect(rows).toHaveLength(1);
    expect(rows[0].classList.contains('acknowledged')).toBe(true);
    expect(rows[0].textContent).toContain('Geofence breach');
  });
});
