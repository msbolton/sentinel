import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { signal } from '@angular/core';
import { EmplacementService } from '../../core/services/emplacement.service';
import { Emplacement } from '../../shared/models/emplacement.model';
import { EmplacementsComponent } from './emplacements.component';
import { AuthService } from '../../core/services/auth.service';
import { FeedService } from '../../core/services/feed.service';

describe('EmplacementsComponent', () => {
  const auth = { hasAnyRole: jest.fn().mockReturnValue(true), getClassificationLevel: jest.fn().mockReturnValue('CONFIDENTIAL') };
  const feeds = {
    feeds: signal([
      { id: 'grpc-1', name: 'Sim', driver: 'grpc', direction: 'in' },
      { id: 'tak-out', name: 'TAK', driver: 'tak', direction: 'out' },
      { id: 'emplacements', name: 'Emplacements', driver: 'emplacement', direction: 'in' },
    ]),
    load: jest.fn(),
  };

  const row = (version: number, name = 'North radar'): Emplacement => ({
    id: 'e1', name, description: '', mobility: 'FIXED', sensorType: 'RADAR', position: { lat: 51, lon: -0.1 }, altitude: null,
    boresightDeg: 0, azimuthReference: 'TRUE_NORTH', coverage: { minRangeM: 0, maxRangeM: 50000, azimuthWidthDeg: 120 },
    defaultSigma: {}, feedId: null, classification: 'UNCLASSIFIED', exercise: false, active: true, lastReportAt: null,
    createdBy: 'u', createdAt: '2026-10-19T00:00:00Z', updatedAt: '2026-10-19T00:00:00Z', version,
  });

  function create(rows: Emplacement[] = []) {
    TestBed.configureTestingModule({
      imports: [EmplacementsComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: auth }, { provide: FeedService, useValue: feeds }],
    });
    const fixture = TestBed.createComponent(EmplacementsComponent);
    const http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http.expectOne('/api/v1/emplacements').flush(rows);
    fixture.detectChanges();
    return { fixture, http, el: fixture.nativeElement as HTMLElement };
  }

  it('offers classifications only up to the user\'s clearance', () => {
    const { fixture, el } = create();
    (el.querySelector('[aria-label="New emplacement"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    const options = Array.from(el.querySelectorAll('select[name="classification"] option')).map((o) => o.textContent?.trim());
    expect(options).toEqual(['UNCLASSIFIED', 'CONFIDENTIAL']);
  });

  it('offers only inbound connections, not the mirror itself', () => {
    const { fixture, el } = create();
    (el.querySelector('[aria-label="New emplacement"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    const options = Array.from(el.querySelectorAll('select[name="feedId"] option')).map((o) => (o as HTMLOptionElement).value);
    expect(options).toEqual(['', 'grpc-1']);
  });

  function typeInto(fixture: { detectChanges(): void }, el: HTMLElement, name: string, value: string) {
    const input = el.querySelector(`input[name="${name}"]`) as HTMLInputElement;
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  }

  it('turns native form validation off and lets the width be zero or fractional', () => {
    const { fixture, el } = create();
    (el.querySelector('[aria-label="New emplacement"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el.querySelector('form.editor')?.hasAttribute('novalidate')).toBe(true);
    const width = el.querySelector('input[name="azimuthWidthDeg"]') as HTMLInputElement;
    expect(width.getAttribute('min')).toBe('0');
    expect(width.getAttribute('step')).toBe('any');
  });

  it('keeps the position unchanged until both coordinates are numbers', () => {
    const { fixture, el } = create();
    const draft = TestBed.inject(EmplacementService).draft;
    (el.querySelector('[aria-label="New emplacement"]') as HTMLButtonElement).click();
    fixture.detectChanges();

    typeInto(fixture, el, 'lat', '51');
    expect(draft()?.position).toBeNull();

    typeInto(fixture, el, 'lon', '-0.12');
    expect(draft()?.position).toEqual({ lat: 51, lon: -0.12 });

    // Chrome reports '' for a number input holding only "-".
    typeInto(fixture, el, 'lon', '');
    expect(draft()?.position).toEqual({ lat: 51, lon: -0.12 });
    expect((el.querySelector('input[name="lat"]') as HTMLInputElement).value).toBe('51');
  });

  it('shows a position picked on the map in the inputs', () => {
    const { fixture, el } = create();
    (el.querySelector('[aria-label="New emplacement"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    TestBed.inject(EmplacementService).pickAt(40.5, -3.25);
    fixture.detectChanges();
    expect((el.querySelector('input[name="lat"]') as HTMLInputElement).value).toBe('40.5');
    expect((el.querySelector('input[name="lon"]') as HTMLInputElement).value).toBe('-3.25');
  });

  it('on a 412 reloads the list, keeps the draft, and the next save sends the fresh version', () => {
    const { fixture, http, el } = create([row(3)]);
    const draft = TestBed.inject(EmplacementService).draft;
    (el.querySelector('.row') as HTMLElement).click();
    fixture.detectChanges();
    typeInto(fixture, el, 'name', 'Renamed');

    (el.querySelector('form.editor') as HTMLFormElement).dispatchEvent(new Event('submit'));
    const first = http.expectOne((r) => r.method === 'PATCH' && r.url === '/api/v1/emplacements/e1');
    expect(first.request.headers.get('If-Match')).toBe('"3"');
    first.flush({ error: 'stale' }, { status: 412, statusText: 'Precondition Failed' });

    http.expectOne({ method: 'GET', url: '/api/v1/emplacements' }).flush([row(4, 'Theirs')]);
    fixture.detectChanges();
    expect(draft()?.name).toBe('Renamed');
    expect(draft()?.version).toBe(4);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('review and save again');

    (el.querySelector('form.editor') as HTMLFormElement).dispatchEvent(new Event('submit'));
    const second = http.expectOne((r) => r.method === 'PATCH' && r.url === '/api/v1/emplacements/e1');
    expect(second.request.headers.get('If-Match')).toBe('"4"');
  });

  // Two editors: B opens version 3, A saves version 4, the map's poll loads
  // it. B's save must still claim the version B edited, so the server refuses
  // it instead of overwriting A's change.
  it('saves with the version the user opened, even after a poll refreshed the list', () => {
    const { fixture, http, el } = create([row(3)]);
    const service = TestBed.inject(EmplacementService);
    (el.querySelector('.row') as HTMLElement).click();
    fixture.detectChanges();
    typeInto(fixture, el, 'name', 'Mine');

    service.load().subscribe();
    http.expectOne({ method: 'GET', url: '/api/v1/emplacements' }).flush([row(4, 'Theirs')]);
    fixture.detectChanges();

    (el.querySelector('form.editor') as HTMLFormElement).dispatchEvent(new Event('submit'));
    const req = http.expectOne((r) => r.method === 'PATCH' && r.url === '/api/v1/emplacements/e1');
    expect(req.request.headers.get('If-Match')).toBe('"3"');
  });

  it('says an emplacement deleted since it was opened is gone, and sends nothing', () => {
    const { fixture, http, el } = create([row(3)]);
    const service = TestBed.inject(EmplacementService);
    (el.querySelector('.row') as HTMLElement).click();
    fixture.detectChanges();

    service.load().subscribe();
    http.expectOne({ method: 'GET', url: '/api/v1/emplacements' }).flush([]);
    fixture.detectChanges();

    (el.querySelector('form.editor') as HTMLFormElement).dispatchEvent(new Event('submit'));
    fixture.detectChanges();
    http.expectNone((r) => r.method === 'PATCH' || r.method === 'POST');
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('Deleted by someone else.');
  });

  it('says so when the reload after a 412 no longer has the emplacement', () => {
    const { fixture, http, el } = create([row(3)]);
    (el.querySelector('.row') as HTMLElement).click();
    fixture.detectChanges();

    (el.querySelector('form.editor') as HTMLFormElement).dispatchEvent(new Event('submit'));
    http.expectOne((r) => r.method === 'PATCH').flush({ error: 'stale' }, { status: 412, statusText: 'Precondition Failed' });
    http.expectOne({ method: 'GET', url: '/api/v1/emplacements' }).flush([]);
    fixture.detectChanges();
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('Deleted by someone else.');
  });

  it('filters by sensor type and by active as well as by mobility', () => {
    const rows: Emplacement[] = [
      { ...row(1, 'Radar on'), id: 'a' },
      { ...row(1, 'Radar off'), id: 'b', active: false },
      { ...row(1, 'AIS on'), id: 'c', sensorType: 'AIS_RECEIVER' },
      { ...row(1, 'Ship radar'), id: 'd', mobility: 'MOBILE' },
    ];
    const { fixture, el } = create(rows);
    const names = () => Array.from(el.querySelectorAll('.row-name')).map((n) => n.textContent?.trim());
    const click = (group: string, label: string) => {
      const buttons = Array.from(el.querySelectorAll(`[aria-label="${group}"] button`)) as HTMLButtonElement[];
      buttons.find((b) => b.textContent?.trim() === label)!.click();
      fixture.detectChanges();
    };
    expect(names()).toEqual(['AIS on', 'Radar off', 'Radar on', 'Ship radar']);

    const sensor = el.querySelector('select[aria-label="Filter by sensor type"]') as HTMLSelectElement;
    expect(sensor.options[0].textContent?.trim()).toBe('All');
    sensor.value = 'RADAR';
    sensor.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    expect(names()).toEqual(['Radar off', 'Radar on', 'Ship radar']);

    click('Filter by state', 'Inactive');
    expect(names()).toEqual(['Radar off']);

    click('Filter by state', 'Active');
    expect(names()).toEqual(['Radar on', 'Ship radar']);

    click('Filter by mobility', 'fixed');
    expect(names()).toEqual(['Radar on']);

    sensor.value = 'ALL';
    sensor.dispatchEvent(new Event('change'));
    click('Filter by state', 'All');
    click('Filter by mobility', 'All');
    expect(names()).toEqual(['AIS on', 'Radar off', 'Radar on', 'Ship radar']);
  });
});
