import { ComponentFixture, TestBed } from '@angular/core/testing';
import { BehaviorSubject, Subject, of } from 'rxjs';
import { tap } from 'rxjs/operators';
import { AlertsComponent } from './alerts.component';
import { AlertService } from '../../core/services/alert.service';
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
          },
        },
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
