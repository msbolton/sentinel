import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { FeedFormComponent, slugify } from './feed-form.component';
import { FeedService } from '../../core/services/feed.service';
import { Feed, FeedDriver } from '../../shared/models/feed.model';

const drivers: FeedDriver[] = [
  {
    id: 'opensky',
    name: 'OpenSky Network',
    description: 'ADS-B states from the OpenSky REST API',
    source: 'OPENSKY',
    capabilities: ['viewport', 'credits'],
    configSchema: {
      type: 'object',
      properties: {
        intervalSec: { type: 'integer', minimum: 1, default: 10, title: 'Poll interval (s)' },
        baseUrl: { type: 'string', format: 'uri' },
      },
    },
    secretFields: ['clientId', 'clientSecret'],
  },
  {
    id: 'stream',
    name: 'Message stream',
    description: 'Bytes over a transport',
    source: 'ADS_B',
    capabilities: [],
    configSchema: {
      type: 'object',
      required: ['transport'],
      properties: {
        transport: { type: 'object', required: ['kind'], properties: { kind: { type: 'string', enum: ['udp', 'tcpclient'] } } },
      },
    },
    secretFields: ['password'],
  },
];

const existing: Feed = {
  id: 'opensky-eu',
  name: 'OpenSky Europe',
  description: 'Europe only',
  driver: 'opensky',
  source: 'OPENSKY',
  builtin: false,
  enabled: false,
  version: 3,
  config: { intervalSec: 30, pinnedReplica: 'ingest-1' },
  secretRefs: { clientSecret: 'env:OPENSKY_CLIENT_SECRET' },
  capabilities: [],
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
  status: {
    owner: '',
    state: 'stopped',
    health: 'unknown',
    lastMessageAt: null,
    lastSuccessAt: null,
    messagesTotal: 0,
    entitiesLast: 0,
    errorsTotal: 0,
    appliedVersion: 3,
    detail: {},
    updatedAt: null,
  },
};

describe('FeedFormComponent', () => {
  let fixture: ComponentFixture<FeedFormComponent>;
  let component: FeedFormComponent;
  let el: HTMLElement;
  let httpMock: HttpTestingController;
  let closed: number;
  let saved: Feed[];

  function type(selector: string, value: string): void {
    const input = el.querySelector<HTMLInputElement>(selector)!;
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  }

  function open(mode: 'create' | 'edit', feed: Feed | null = null): void {
    fixture.componentRef.setInput('mode', mode);
    fixture.componentRef.setInput('feed', feed);
    fixture.detectChanges();
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [FeedFormComponent],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    TestBed.inject(FeedService).loadDrivers().subscribe();
    httpMock.expectOne('/api/v1/drivers').flush(drivers);

    fixture = TestBed.createComponent(FeedFormComponent);
    component = fixture.componentInstance;
    el = fixture.nativeElement as HTMLElement;
    closed = 0;
    saved = [];
    component.closed.subscribe(() => closed++);
    component.saved.subscribe((f) => saved.push(f));
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('slugifies names the way the server does', () => {
    expect(slugify('OpenSky Europe')).toBe('opensky-europe');
    expect(slugify('  ADS-B / Home (USB) ')).toBe('ads-b-home-usb');
    expect(slugify('x'.repeat(80))).toHaveLength(63);
  });

  it('create: lists the drivers first, then the fields for the chosen one', () => {
    open('create');

    const cards = el.querySelectorAll('.driver-card');
    expect(cards.length).toBe(2);
    expect(cards[0].querySelector('.driver-name')?.textContent?.trim()).toBe('OpenSky Network');
    expect(cards[0].querySelectorAll('.chip').length).toBe(2);
    expect(el.querySelector('form')).toBeNull();
    expect(el.querySelector('.drawer-footer')).toBeNull();

    (cards[0] as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(component.step()).toBe(2);
    expect(el.querySelector('h2')?.textContent).toContain('OpenSky Network');
    expect(el.querySelector('app-schema-form input[name="intervalSec"]')).toBeTruthy();
    expect(el.querySelector('app-schema-form input[name="clientSecret"]')).toBeTruthy();
    expect(el.querySelector<HTMLInputElement>('#feed-id')?.disabled).toBe(false);

    type('#feed-name', 'OpenSky Europe');
    expect(el.querySelector('.help code')?.textContent).toBe('opensky-europe');
    type('#feed-id', 'my-id');
    expect(el.querySelector('.help code')?.textContent).toBe('my-id');
    type('#feed-id', 'Bad Id');
    expect(component.form.controls.id.hasError('pattern')).toBe(true);
  });

  it('create: posts the feed, maps 422 problems to fields, then closes on success', () => {
    open('create');
    component.selectDriver('opensky');
    fixture.detectChanges();

    type('#feed-name', 'OpenSky Europe');
    type('app-schema-form input[name="intervalSec"]', '5');
    type('app-schema-form input[name="clientSecret"]', 'env:OPENSKY_CLIENT_SECRET');
    expect(component.canSubmit()).toBe(true);

    el.querySelector<HTMLFormElement>('form')!.dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/v1/feeds');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({
      id: undefined,
      name: 'OpenSky Europe',
      description: undefined,
      driver: 'opensky',
      config: { intervalSec: 5 },
      secretRefs: { clientSecret: 'env:OPENSKY_CLIENT_SECRET' },
      enabled: true,
    });
    req.flush(
      {
        error: 'validation failed',
        details: [
          { path: '/config/intervalSec', message: 'must be >= 10 for anonymous access' },
          { path: '/secretRefs/clientSecret', message: 'variable not set' },
          { path: '/name', message: 'taken' },
          { path: '/somewhere', message: 'odd' },
        ],
      },
      { status: 422, statusText: 'Unprocessable Entity' },
    );
    fixture.detectChanges();

    expect(component.saving()).toBe(false);
    expect(el.querySelector('.form-error')?.textContent).toContain('validation failed');
    expect(el.querySelector('[data-pointer="/config/intervalSec"] .sf-error-server')?.textContent).toContain(
      'must be >= 10 for anonymous access',
    );
    expect(el.querySelector('[data-pointer="/secretRefs/clientSecret"] .sf-error-server')?.textContent).toContain('variable not set');
    expect(el.textContent).toContain('Server: taken');
    expect(el.querySelector('.problem-list')?.textContent).toContain('/somewhere');
    expect(closed).toBe(0);

    // fix and resubmit
    component.submit();
    const retry = httpMock.expectOne('/api/v1/feeds');
    const created: Feed = { ...existing, id: 'opensky-europe', name: 'OpenSky Europe', enabled: true };
    retry.flush(created);
    fixture.detectChanges();

    expect(saved).toEqual([created]);
    expect(closed).toBe(1);
    expect(TestBed.inject(FeedService).feed('opensky-europe')).toEqual(created);
  });

  it('create: explains a duplicate id', () => {
    open('create');
    component.selectDriver('opensky');
    fixture.detectChanges();
    type('#feed-name', 'Dup');
    component.submit();

    httpMock.expectOne('/api/v1/feeds').flush({ error: 'feed already exists' }, { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();

    expect(el.querySelector('.form-error')?.textContent).toContain('A feed with id "dup" already exists');
  });

  it('refuses to submit while invalid and surfaces every error', () => {
    open('create');
    component.selectDriver('stream');
    fixture.detectChanges();

    component.submit();
    fixture.detectChanges();

    httpMock.expectNone('/api/v1/feeds');
    expect(el.querySelector('.form-error')?.textContent).toContain('Fix the highlighted fields');
    expect(el.querySelector('#feed-name + .error')?.textContent).toContain('Name is required');
    expect(el.querySelector('[data-pointer="/config/transport/kind"] .sf-error')?.textContent).toContain('Required');
  });

  it('edit: prefills from the feed, fixes the driver and PUTs every mutable field', () => {
    open('edit', existing);

    expect(component.step()).toBe(2);
    expect(el.querySelector('.driver-card')).toBeNull();
    expect(el.querySelector('.driver-summary .driver-name')?.textContent?.trim()).toBe('OpenSky Network');
    expect(el.querySelector<HTMLInputElement>('#feed-name')?.value).toBe('OpenSky Europe');
    expect(el.querySelector<HTMLInputElement>('#feed-id')?.value).toBe('opensky-eu');
    expect(el.querySelector<HTMLInputElement>('#feed-id')?.disabled).toBe(true);
    expect(el.querySelector<HTMLInputElement>('#feed-description')?.value).toBe('Europe only');
    expect(el.querySelector<HTMLInputElement>('input[formControlName="enabled"]')?.checked).toBe(false);
    expect(el.querySelector<HTMLInputElement>('app-schema-form input[name="intervalSec"]')?.value).toBe('30');
    expect(el.querySelector<HTMLInputElement>('app-schema-form input[name="clientSecret"]')?.value).toBe('env:OPENSKY_CLIENT_SECRET');
    expect(el.querySelector<HTMLInputElement>('app-schema-form input[name="pinnedReplica"]')?.value).toBe('ingest-1');

    type('#feed-name', 'OpenSky EU');
    component.submit();

    const req = httpMock.expectOne('/api/v1/feeds/opensky-eu');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({
      name: 'OpenSky EU',
      description: 'Europe only',
      config: { intervalSec: 30, pinnedReplica: 'ingest-1' },
      secretRefs: { clientSecret: 'env:OPENSKY_CLIENT_SECRET' },
      enabled: false,
    });
    req.flush({ ...existing, name: 'OpenSky EU', version: 4 });
    fixture.detectChanges();

    expect(saved[0]?.version).toBe(4);
    expect(closed).toBe(1);
  });

  it('closes on Escape, backdrop click and Cancel, but not while saving', () => {
    open('create');

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(closed).toBe(1);

    el.querySelector<HTMLElement>('.drawer-backdrop')?.click();
    expect(closed).toBe(2);

    component.selectDriver('opensky');
    fixture.detectChanges();
    Array.from(el.querySelectorAll<HTMLButtonElement>('.drawer-footer .btn')).find((b) => b.textContent?.trim() === 'Cancel')?.click();
    expect(closed).toBe(3);

    component.saving.set(true);
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(closed).toBe(3);
  });
});
