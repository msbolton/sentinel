import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Subject, of } from 'rxjs';
import { AlertsComponent } from './alerts.component';
import { AlertService } from '../../core/services/alert.service';
import { EntityFocusService, FocusTarget } from '../../core/services/entity-focus.service';
import { Alert, AlertSeverity, AlertType } from '../../shared/models/alert.model';

function makeAlert(over: Partial<Alert>): Alert {
  return {
    id: 'a1', title: 'Geofence breach', message: 'Entered zone', severity: AlertSeverity.HIGH, alertType: AlertType.GEOFENCE_ENTRY,
    acknowledged: false, metadata: {}, createdAt: '2026-10-18T12:00:00Z', updatedAt: '2026-10-18T12:00:00Z',
    ...over,
  };
}

describe('AlertsComponent', () => {
  let fixture: ComponentFixture<AlertsComponent>;
  let el: HTMLElement;
  let focused: FocusTarget[];

  const alerts = [
    makeAlert({ id: 'with-entity', entityId: 'e1', entityName: 'UAL1', position: { latitude: 41.9, longitude: -87.9 } }),
    makeAlert({ id: 'position-only', title: 'Range ring', position: { latitude: 50, longitude: 1 } }),
    makeAlert({ id: 'nothing', title: 'Feed down' }),
  ];

  beforeEach(() => {
    const alertService = {
      alertStream$: new Subject<Alert>(),
      unacknowledgedCount$: of(3),
      getAlerts: () => of({ data: alerts, total: alerts.length }),
      acknowledgeAlert: (id: string) => of({ ...alerts.find((a) => a.id === id)!, acknowledged: true }),
    };
    TestBed.configureTestingModule({
      imports: [AlertsComponent],
      providers: [{ provide: AlertService, useValue: alertService }],
    });
    focused = [];
    TestBed.inject(EntityFocusService).focus$.subscribe((t) => focused.push(t));
    fixture = TestBed.createComponent(AlertsComponent);
    el = fixture.nativeElement;
    fixture.detectChanges();
  });

  const cards = () => Array.from(el.querySelectorAll('.alert-card')) as HTMLElement[];

  it('makes cards with an entity or a position clickable, and only those', () => {
    const [entity, position, nothing] = cards();
    expect(entity.classList).toContain('clickable');
    expect(entity.getAttribute('role')).toBe('button');
    expect(entity.getAttribute('tabindex')).toBe('0');
    expect(entity.getAttribute('aria-label')).toBe('Show UAL1 on the map');
    expect(position.classList).toContain('clickable');
    expect(nothing.classList).not.toContain('clickable');
    expect(nothing.getAttribute('role')).toBeNull();
    expect(nothing.querySelector('.show-on-map')).toBeNull();
  });

  it('shows the entity on the map when its card is clicked', () => {
    cards()[0].click();
    expect(focused).toEqual([{ entityId: 'e1', position: { latitude: 41.9, longitude: -87.9 }, label: 'UAL1' }]);
    cards()[1].click();
    expect(focused[1]).toEqual({ entityId: undefined, position: { latitude: 50, longitude: 1 }, label: 'Range ring' });
    cards()[2].click();
    expect(focused.length).toBe(2);
  });

  it('shows once from the Show on map button, and not at all from Acknowledge', () => {
    (cards()[0].querySelector('.show-on-map') as HTMLButtonElement).click();
    expect(focused.length).toBe(1);
    const ack = Array.from(cards()[0].querySelectorAll('button')).find((b) => b.textContent?.includes('Acknowledge')) as HTMLButtonElement;
    ack.click();
    expect(focused.length).toBe(1);
  });

  it('shows on Enter on the card, but not on Enter on a button inside it', () => {
    cards()[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(focused.length).toBe(1);
    const ack = Array.from(cards()[0].querySelectorAll('button')).find((b) => b.textContent?.includes('Acknowledge')) as HTMLButtonElement;
    ack.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(focused.length).toBe(1);
  });
});
