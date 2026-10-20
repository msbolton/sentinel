import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { PolicyChange, PolicyEditorComponent } from './policy-editor.component';
import { FeedService } from '../../core/services/feed.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { ConnectionPolicy, Feed } from '../../shared/models/feed.model';

function makeFeed(id: string, over: Partial<Feed> = {}): Feed {
  return {
    id,
    name: id.toUpperCase(),
    description: '',
    driver: 'opensky',
    source: 'OPENSKY',
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
    ...over,
  };
}

describe('PolicyEditorComponent', () => {
  let fixture: ComponentFixture<PolicyEditorComponent>;
  let component: PolicyEditorComponent;
  let el: HTMLElement;
  let httpMock: HttpTestingController;
  let changes: PolicyChange[];

  function last(): PolicyChange {
    return changes[changes.length - 1];
  }

  function render(value: ConnectionPolicy | undefined = undefined, clearance = 'TOP_SECRET'): void {
    fixture.componentRef.setInput('value', value);
    fixture.componentRef.setInput('clearance', clearance);
    fixture.detectChanges();
  }

  function setNumber(name: string, value: string): void {
    const input = el.querySelector<HTMLInputElement>(`input[name="${name}"]`)!;
    input.value = value;
    input.dispatchEvent(new Event('input'));
    input.dispatchEvent(new Event('blur'));
    fixture.detectChanges();
  }

  function checkbox(name: string, value: string): HTMLInputElement {
    return el.querySelector<HTMLInputElement>(`input[name="${name}"][value="${value}"]`)!;
  }

  function button(text: string): HTMLButtonElement {
    return Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text)!;
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [PolicyEditorComponent],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    TestBed.inject(FeedService).load().subscribe();
    httpMock.expectOne('/api/v1/feeds').flush([
      makeFeed('opensky', { direction: 'in' }),
      makeFeed('tak-hq', { direction: 'both' }),
      makeFeed('tak-out', { direction: 'out' }),
      makeFeed('legacy'),
    ]);

    fixture = TestBed.createComponent(PolicyEditorComponent);
    component = fixture.componentInstance;
    el = fixture.nativeElement as HTMLElement;
    changes = [];
    component.changed.subscribe((c) => changes.push(c));
  });

  afterEach(() => httpMock.verify());

  it('starts empty, valid, and offers feeds that produce entities', () => {
    render();

    expect(changes.length).toBe(1);
    expect(last()).toEqual({ value: {}, valid: true });
    expect(el.querySelectorAll('input[name="entityType"]').length).toBe(12);
    expect(Array.from(el.querySelectorAll<HTMLInputElement>('input[name="policyFeed"]')).map((i) => i.value)).toEqual([
      'opensky',
      'tak-hq',
      'legacy',
    ]);
  });

  it('emits entity types, feeds, flags and alerts the way the server stores them', () => {
    render();

    checkbox('entityType', 'AIRCRAFT').click();
    checkbox('entityType', 'VESSEL').click();
    checkbox('policyFeed', 'opensky').click();
    fixture.detectChanges();
    expect(last().value).toEqual({ entityTypes: ['AIRCRAFT', 'VESSEL'], feeds: ['opensky'] });

    checkbox('entityType', 'AIRCRAFT').click();
    fixture.detectChanges();
    expect(last().value.entityTypes).toEqual(['VESSEL']);

    component.form.controls.reexport.setValue(true);
    component.form.controls.allowExercise.setValue(true);
    component.form.controls.alertsEnabled.setValue(true);
    component.form.controls.alertsMinSeverity.setValue('HIGH');
    component.form.controls.classificationCeiling.setValue('SECRET');
    fixture.detectChanges();
    expect(last().value).toEqual({
      entityTypes: ['VESSEL'],
      feeds: ['opensky'],
      classificationCeiling: 'SECRET',
      reexport: true,
      allowExercise: true,
      alerts: { enabled: true, minSeverity: 'HIGH' },
    });
    expect(last().valid).toBe(true);
  });

  it('validates area boxes: ranges, south <= north, and the 16 box cap', () => {
    render();

    button('Add area').click();
    fixture.detectChanges();
    expect(last().valid).toBe(false); // empty fields are required
    expect(last().value.boxes).toBeUndefined();

    setNumber('box-0-north', '95');
    setNumber('box-0-south', '10');
    setNumber('box-0-east', '20');
    setNumber('box-0-west', '-200');
    expect(component.boxFieldHasIssue(0, 'north')).toBe(true);
    expect(component.boxFieldHasIssue(0, 'west')).toBe(true);
    expect(component.boxErrors(0)).toEqual(['North must be at most 90.', 'West must be at least -180.']);

    setNumber('box-0-north', '5');
    setNumber('box-0-west', '30');
    expect(component.boxErrors(0)).toEqual(['South must not be above north.']);
    expect(last().valid).toBe(false);

    setNumber('box-0-north', '15');
    expect(component.boxErrors(0)).toEqual([]);
    expect(last().valid).toBe(true);
    // west > east is allowed: it crosses the antimeridian
    expect(last().value.boxes).toEqual([{ north: 15, south: 10, east: 20, west: 30 }]);

    for (let i = 1; i < 16; i++) {
      component.addBox({ north: 1, south: 0, east: 1, west: 0 });
    }
    fixture.detectChanges();
    expect(component.boxCount()).toBe(16);
    expect(button('Add area').disabled).toBe(true);
    component.addBox({ north: 1, south: 0, east: 1, west: 0 });
    expect(component.boxCount()).toBe(16);

    button('Remove').click();
    fixture.detectChanges();
    expect(component.boxCount()).toBe(15);
    expect(button('Add area').disabled).toBe(false);
  });

  it('adds the last map view as an area, and is disabled until the map has reported one', () => {
    render();

    const useView = button('Use last map view');
    expect(useView.disabled).toBe(true);
    expect(useView.parentElement?.getAttribute('title')).toContain('Open the map first');

    TestBed.inject(MapViewportService).set({ north: 51.123456, south: 50.1, east: 1.5, west: -0.5 });
    fixture.detectChanges();
    expect(button('Use last map view').disabled).toBe(false);

    button('Use last map view').click();
    fixture.detectChanges();
    expect(last().value.boxes).toEqual([{ north: 51.1235, south: 50.1, east: 1.5, west: -0.5 }]);
    expect(last().valid).toBe(true);
  });

  it('caps the classification ceiling at the clearance', () => {
    render(undefined, 'SECRET');

    const options = Array.from(el.querySelectorAll<HTMLOptionElement>('#pe-ceiling option'));
    expect(options.map((o) => o.value)).toEqual(['', 'UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET']);
    expect(options[0].textContent).toContain('Default (UNCLASSIFIED)');

    fixture.componentRef.setInput('clearance', 'classification-u');
    fixture.detectChanges();
    expect(Array.from(el.querySelectorAll<HTMLOptionElement>('#pe-ceiling option')).map((o) => o.value)).toEqual(['', 'UNCLASSIFIED']);
  });

  it('keeps a stored ceiling above the clearance as a disabled option', () => {
    render({ classificationCeiling: 'TOP_SECRET' }, 'SECRET');

    const options = Array.from(el.querySelectorAll<HTMLOptionElement>('#pe-ceiling option'));
    const kept = options.find((o) => o.value === 'TOP_SECRET');
    expect(kept?.disabled).toBe(true);
    expect(el.querySelector<HTMLSelectElement>('#pe-ceiling')?.value).toBe('TOP_SECRET');
    expect(el.textContent).toContain('Set above your clearance');
    expect(last().value.classificationCeiling).toBe('TOP_SECRET');
  });

  it('prefills from a stored policy and carries alert types through', () => {
    render({
      entityTypes: ['AIRCRAFT'],
      feeds: ['opensky', 'gone'],
      boxes: [{ north: 60, south: 50, east: 10, west: 0 }],
      classificationCeiling: 'CONFIDENTIAL',
      reexport: true,
      allowExercise: true,
      alerts: { enabled: true, minSeverity: 'MEDIUM', types: ['GEOFENCE'] },
    });

    expect(checkbox('entityType', 'AIRCRAFT').checked).toBe(true);
    expect(checkbox('entityType', 'VESSEL').checked).toBe(false);
    expect(checkbox('policyFeed', 'opensky').checked).toBe(true);
    expect(checkbox('policyFeed', 'gone').checked).toBe(true);
    expect(el.textContent).toContain('no longer exists');
    expect(el.querySelector<HTMLInputElement>('input[name="box-0-north"]')?.value).toBe('60');
    expect(el.querySelector<HTMLInputElement>('input[name="box-0-west"]')?.value).toBe('0');
    expect(el.querySelector<HTMLSelectElement>('#pe-ceiling')?.value).toBe('CONFIDENTIAL');
    expect(el.querySelector<HTMLInputElement>('input[name="reexport"]')?.checked).toBe(true);
    expect(el.querySelector<HTMLInputElement>('input[name="allowExercise"]')?.checked).toBe(true);
    expect(el.querySelector<HTMLInputElement>('input[name="alertsEnabled"]')?.checked).toBe(true);
    expect(el.querySelector<HTMLSelectElement>('#pe-alert-severity')?.value).toBe('MEDIUM');

    expect(last()).toEqual({
      valid: true,
      value: {
        entityTypes: ['AIRCRAFT'],
        feeds: ['opensky', 'gone'],
        boxes: [{ north: 60, south: 50, east: 10, west: 0 }],
        classificationCeiling: 'CONFIDENTIAL',
        reexport: true,
        allowExercise: true,
        alerts: { enabled: true, minSeverity: 'MEDIUM', types: ['GEOFENCE'] },
      },
    });

    el.querySelector<HTMLInputElement>('input[name="allowExercise"]')!.click();
    fixture.detectChanges();
    expect(last().value).not.toHaveProperty('allowExercise');
  });

  it('routes server problems to the box field, the section, or the top of the editor', () => {
    render({ boxes: [{ north: 1, south: 0, east: 1, west: 0 }, { north: 1, south: 0, east: 1, west: 0 }] });

    fixture.componentRef.setInput('problems', [
      { path: '/config/policy/boxes/1/south', message: 'must be <= north' },
      { path: '/config/policy/boxes', message: 'too many' },
      { path: '/config/policy/entityTypes/0', message: 'unknown type' },
      { path: '/config/policy/classificationCeiling', message: 'above your clearance' },
      { path: '/config/policy/allowExercise', message: 'must be a boolean' },
      { path: '/config/policy', message: 'policy rejected' },
      { path: '/config/intervalSec', message: 'not ours' },
    ]);
    fixture.detectChanges();

    expect(el.querySelector('[data-pointer="/config/policy/boxes/1/south"]')?.classList.contains('has-issue')).toBe(true);
    expect(el.querySelector('[data-pointer="/config/policy/boxes/0/south"]')?.classList.contains('has-issue')).toBe(false);
    expect(el.querySelector('[data-pointer="/config/policy/boxes/1"] .pe-error-server')?.textContent).toContain('south: must be <= north');
    expect(component.sectionProblems('boxes')).toEqual(['too many']);
    expect(component.sectionProblems('entityTypes')).toEqual(['unknown type']);
    expect(component.sectionProblems('classificationCeiling')).toEqual(['above your clearance']);
    expect(component.sectionProblems('allowExercise')).toEqual(['must be a boolean']);
    expect(el.querySelector('[data-pointer="/config/policy/allowExercise"] .pe-error-server')?.textContent).toContain('must be a boolean');
    expect(el.querySelector('.pe-problems')?.textContent).not.toContain('must be a boolean');
    expect(el.querySelector('.pe-problems')?.textContent).toContain('policy rejected');
    expect(el.querySelector('.pe-problems')?.textContent).not.toContain('not ours');
  });

  it('disables every control', () => {
    render({ boxes: [{ north: 1, south: 0, east: 1, west: 0 }] });
    fixture.componentRef.setInput('disabled', true);
    fixture.detectChanges();

    expect(checkbox('entityType', 'AIRCRAFT').disabled).toBe(true);
    expect(el.querySelector<HTMLInputElement>('input[name="box-0-north"]')?.disabled).toBe(true);
    expect(el.querySelector<HTMLSelectElement>('#pe-ceiling')?.disabled).toBe(true);
    expect(button('Add area').disabled).toBe(true);
  });
});
