import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { of } from 'rxjs';
import { GeofencesComponent } from './geofences.component';
import { GeofenceService } from '../../core/services/geofence.service';
import { AuthService } from '../../core/services/auth.service';
import { EntityService } from '../../core/services/entity.service';
import { GeofenceDraft, GeofenceRule } from '../../shared/models/geofence.model';

const gulf: GeofenceRule = {
  id: 'r1', name: 'Persian Gulf exclusion zone', ruleType: 'GEOFENCE',
  config: { shape: 'polygon', polygon: [[51, 27], [56, 27], [56, 24], [51, 24]], triggerOn: 'ENTRY', color: '#ef4444' },
  monitoredEntityTypes: ['VESSEL', 'AIRCRAFT'], severity: 'HIGH', enabled: true,
  createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z',
};
const port: GeofenceRule = {
  ...gulf, id: 'r2', name: 'Port approach', monitoredEntityTypes: ['VESSEL'], enabled: false,
  config: { shape: 'circle', polygon: [[55.4, 25.3]], circle: { center: [55.3, 25.3], radiusM: 15000 }, triggerOn: 'ENTRY', color: '#06b6d4' },
};

describe('GeofencesComponent', () => {
  let fixture: ComponentFixture<GeofencesComponent>;
  let el: HTMLElement;
  let http: HttpTestingController;
  let service: GeofenceService;
  const auth = { hasAnyRole: jest.fn() };
  const entities = { getEntity: jest.fn().mockReturnValue(of(null)) };

  const button = (text: string) => Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
  const chip = (label: string) => Array.from(el.querySelectorAll<HTMLButtonElement>('[role="group"][aria-label="Watch"] button')).find((b) => b.textContent?.trim() === label)!;
  const rows = () => Array.from(el.querySelectorAll('.zone-row'));

  async function setup(writer: boolean, zones: GeofenceRule[]) {
    auth.hasAnyRole.mockReturnValue(writer);
    await TestBed.configureTestingModule({
      imports: [GeofencesComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: auth }, { provide: EntityService, useValue: entities }],
    }).compileComponents();
    http = TestBed.inject(HttpTestingController);
    service = TestBed.inject(GeofenceService);
    fixture = TestBed.createComponent(GeofencesComponent);
    el = fixture.nativeElement as HTMLElement;
    fixture.detectChanges();
    http.expectOne('/api/v1/alerts/rules').flush(zones);
    fixture.detectChanges();
  }

  afterEach(() => http.verify());

  it('lists zones with their summaries and switches', async () => {
    await setup(true, [gulf, port]);
    expect(rows().map((r) => r.querySelector('.zone-name')?.textContent?.trim())).toEqual(['Persian Gulf exclusion zone', 'Port approach']);
    expect(rows()[1].querySelector('.zone-summary')?.textContent?.trim()).toBe('enter · vessels · HIGH · 15.0 km');
    expect(rows().map((r) => r.querySelector('[role="switch"]')?.getAttribute('aria-checked'))).toEqual(['true', 'false']);
    expect(el.querySelector('.panel-header')?.textContent).toContain('2 · 1 active');
    (rows()[1].querySelector('[role="switch"]') as HTMLButtonElement).click();
    const req = http.expectOne('/api/v1/alerts/rules/r2');
    expect(req.request.body).toEqual({ enabled: true });
    req.flush({ ...port, enabled: true });
  });

  it('hides New, the editor and Delete from a viewer', async () => {
    await setup(false, [gulf]);
    expect(button('New')).toBeUndefined();
    (rows()[0] as HTMLElement).click();
    fixture.detectChanges();
    expect(el.querySelector('.zone-editor')).toBeNull();
    expect(button('Delete')).toBeUndefined();
    expect(service.selectedId()).toBe('r1');
    expect(el.querySelector<HTMLButtonElement>('.zone-switch')!.disabled).toBe(true);
    const hint = el.querySelector('.footer-hint')!.textContent!;
    expect(hint).not.toMatch(/reshape|new zone/);
    expect(hint).toContain('Click one to select it');
  });

  it('opens the editor for the selected zone and saves only what changed', async () => {
    await setup(true, [gulf]);
    (rows()[0] as HTMLElement).click();
    fixture.detectChanges();
    const name = el.querySelector<HTMLInputElement>('.zone-editor input[aria-label="Name"]')!;
    expect(name.value).toBe('Persian Gulf exclusion zone');
    expect(document.activeElement).toBe(name);
    button('Leaves')!.click();
    fixture.detectChanges();
    button('Save geofence')!.click();
    const req = http.expectOne('/api/v1/alerts/rules/r1');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ config: { ...gulf.config, triggerOn: 'EXIT' } });
    req.flush({ ...gulf, config: { ...gulf.config, triggerOn: 'EXIT' } });
    fixture.detectChanges();
    expect(rows()[0].querySelector('.zone-summary')?.textContent?.trim()).toMatch(/^leave · vessels, aircraft · HIGH/);
  });

  it('creates from a draft with the chosen types and severity', async () => {
    await setup(true, []);
    const draft: GeofenceDraft = {
      name: 'New geofence', ruleType: 'GEOFENCE',
      config: { shape: 'polygon', polygon: [[55, 25], [56, 25], [56, 24]], triggerOn: 'ENTRY', color: '#f97316' },
      monitoredEntityTypes: [], severity: 'HIGH', enabled: true,
    };
    service.setDraft(draft);
    fixture.detectChanges();
    const name = el.querySelector<HTMLInputElement>('.zone-editor input[aria-label="Name"]')!;
    name.value = 'Anchorage north';
    name.dispatchEvent(new Event('input'));
    chip('Vessels').click();
    chip('Aircraft').click();
    const severity = el.querySelector<HTMLSelectElement>('select[aria-label="Severity"]')!;
    severity.value = 'MEDIUM';
    severity.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    button('Save geofence')!.click();
    const req = http.expectOne('/api/v1/alerts/rules');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ ...draft, name: 'Anchorage north', monitoredEntityTypes: ['VESSEL', 'AIRCRAFT'], severity: 'MEDIUM' });
    req.flush({ ...req.request.body, id: 'r9', createdAt: 'now', updatedAt: 'now' });
    fixture.detectChanges();
    expect(service.draft()).toBeNull();
    expect(service.selectedId()).toBe('r9');
    expect(rows()).toHaveLength(1);
  });

  it('clears the watched types when Everything is chosen', async () => {
    await setup(true, [port]);
    (rows()[0] as HTMLElement).click();
    fixture.detectChanges();
    expect(chip('Vessels').getAttribute('aria-pressed')).toBe('true');
    chip('Everything').click();
    fixture.detectChanges();
    expect(chip('Vessels').getAttribute('aria-pressed')).toBe('false');
    expect(chip('Everything').getAttribute('aria-pressed')).toBe('true');
    button('Save geofence')!.click();
    const req = http.expectOne('/api/v1/alerts/rules/r2');
    expect(req.request.body).toEqual({ monitoredEntityTypes: [] });
    req.flush({ ...port, monitoredEntityTypes: [] });
  });

  it('starts a new zone on N outside a field', async () => {
    await setup(true, [gulf]);
    let requests = 0;
    service.startDrawingRequested.subscribe(() => requests++);
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'n', bubbles: true }));
    expect(requests).toBe(1);
    (rows()[0] as HTMLElement).click();
    fixture.detectChanges();
    el.querySelector<HTMLInputElement>('.zone-editor input[aria-label="Name"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'n', bubbles: true }));
    expect(requests).toBe(1);
    button('New')!.click();
    expect(requests).toBe(2);
  });
});
