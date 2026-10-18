import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ConnectionFormComponent, slugify } from './connection-form.component';
import { AuthService } from '../../core/services/auth.service';
import { FeedService } from '../../core/services/feed.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
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
  {
    id: 'tak',
    name: 'TAK Server',
    description: 'Cursor on Target over TLS',
    source: 'COT',
    direction: 'both',
    capabilities: ['send'],
    configSchema: {
      type: 'object',
      required: ['host'],
      properties: { host: { type: 'string', minLength: 1 }, port: { type: 'integer', default: 8089 } },
    },
    secretFields: ['clientCert'],
  },
  {
    id: 'kml-push',
    name: 'KML push',
    description: 'Sends KML to a URL',
    source: 'KML',
    direction: 'out',
    capabilities: ['send'],
    configSchema: { type: 'object', properties: { url: { type: 'string', format: 'uri' } } },
    secretFields: [],
  },
];

/**
 * The TCP driver, trimmed to what the create flow needs: `mode` is required and defaults to
 * listen. Not in `drivers`: the test that wants it loads the catalog again with it.
 */
const tcpDriver: FeedDriver = {
  id: 'tcp',
  name: 'TCP',
  description: 'An external system sends data over TCP',
  source: '',
  direction: 'in',
  capabilities: ['listen'],
  configSchema: {
    type: 'object',
    required: ['mode'],
    properties: {
      mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen', title: 'Mode' },
      host: { type: 'string', minLength: 1, title: 'Host', 'x-showWhen': { mode: 'connect' } },
      port: { type: 'integer', minimum: 1, maximum: 65535, title: 'Port' },
    },
  },
  secretFields: [],
};

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

describe('ConnectionFormComponent', () => {
  let fixture: ComponentFixture<ConnectionFormComponent>;
  let component: ConnectionFormComponent;
  let el: HTMLElement;
  let httpMock: HttpTestingController;
  let closed: number;
  let saved: Feed[];
  let clearance: string;

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
    clearance = 'SECRET';
    await TestBed.configureTestingModule({
      imports: [ConnectionFormComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { getClassificationLevel: () => clearance } },
      ],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    TestBed.inject(FeedService).loadDrivers().subscribe();
    httpMock.expectOne('/api/v1/drivers').flush(drivers);

    fixture = TestBed.createComponent(ConnectionFormComponent);
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
    expect(cards.length).toBe(4);
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
    expect(el.querySelector('app-policy-editor')).toBeNull();
    expect(el.querySelector('#feed-direction')).toBeNull();

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
      config: { intervalSec: 5, classification: 'UNCLASSIFIED' },
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

    expect(el.querySelector('.form-error')?.textContent).toContain('A connection with id "dup" already exists');
  });

  it('create: a new TCP connection pre-selects listen and saves it', () => {
    TestBed.inject(FeedService).loadDrivers().subscribe();
    httpMock.expectOne('/api/v1/drivers').flush([...drivers, tcpDriver]);

    open('create');
    component.selectDriver('tcp');
    fixture.detectChanges();

    const mode = el.querySelector<HTMLSelectElement>('app-schema-form select[name="mode"]')!;
    expect(component.schemaForm()?.control('/config/mode')?.value).toBe('listen');
    expect(mode.options[mode.selectedIndex].textContent?.trim()).toBe('listen');

    type('#feed-name', 'Radar feed');
    type('app-schema-form input[name="port"]', '6001');
    expect(component.canSubmit()).toBe(true);

    component.submit();
    const req = httpMock.expectOne('/api/v1/feeds');
    expect(req.request.method).toBe('POST');
    expect(req.request.body.driver).toBe('tcp');
    expect(req.request.body.config).toEqual(expect.objectContaining({ mode: 'listen', port: 6001 }));
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
    expect(el.querySelector<HTMLSelectElement>('#feed-classification')?.value).toBe('UNCLASSIFIED');

    type('#feed-name', 'OpenSky EU');
    component.submit();

    const req = httpMock.expectOne('/api/v1/feeds/opensky-eu');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({
      name: 'OpenSky EU',
      description: 'Europe only',
      config: { intervalSec: 30, pinnedReplica: 'ingest-1', classification: 'UNCLASSIFIED' },
      secretRefs: { clientSecret: 'env:OPENSKY_CLIENT_SECRET' },
      enabled: false,
    });
    req.flush({ ...existing, name: 'OpenSky EU', version: 4 });
    fixture.detectChanges();

    expect(saved[0]?.version).toBe(4);
    expect(closed).toBe(1);
  });

  it('offers classifications up to the clearance and keeps a stored level above it', () => {
    open('create');
    component.selectDriver('opensky');
    fixture.detectChanges();

    const options = () => Array.from(el.querySelectorAll<HTMLOptionElement>('#feed-classification option'));
    expect(options().map((o) => o.value)).toEqual(['UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET']);
    expect(options().every((o) => !o.disabled)).toBe(true);

    el.querySelector<HTMLSelectElement>('#feed-classification')!.value = 'CONFIDENTIAL';
    el.querySelector<HTMLSelectElement>('#feed-classification')!.dispatchEvent(new Event('change'));
    type('#feed-name', 'Confidential feed');
    component.submit();
    expect(httpMock.expectOne('/api/v1/feeds').request.body.config).toEqual({ classification: 'CONFIDENTIAL' });

    open('edit', { ...existing, config: { ...existing.config, classification: 'TOP_SECRET' } });
    expect(el.querySelector<HTMLSelectElement>('#feed-classification')?.value).toBe('TOP_SECRET');
    expect(options().find((o) => o.value === 'TOP_SECRET')?.disabled).toBe(true);
    expect(el.textContent).toContain('Set above your clearance');
    expect(component.canSubmit()).toBe(true);
  });

  it('shows the policy editor for a sending driver and saves its policy with the config', () => {
    open('create');
    component.selectDriver('kml-push');
    fixture.detectChanges();

    const editor = el.querySelector('app-policy-editor');
    expect(editor).toBeTruthy();
    expect(el.querySelector('#feed-direction')).toBeNull();
    // a sink keeps delivery tuning in Advanced; a source never sees it
    expect(component.externalKeys()).toEqual(['classification', 'direction', 'policy']);

    type('#feed-name', 'KML to the ops room');
    editor!.querySelector<HTMLInputElement>('input[name="entityType"][value="AIRCRAFT"]')!.click();
    TestBed.inject(MapViewportService).set({ north: 10, south: 0, east: 10, west: 0 });
    fixture.detectChanges();
    Array.from(editor!.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === 'Use last map view')!.click();
    fixture.detectChanges();
    expect(component.canSubmit()).toBe(true);

    component.submit();
    const req = httpMock.expectOne('/api/v1/feeds');
    expect(req.request.body.driver).toBe('kml-push');
    expect(req.request.body.config).toEqual({
      classification: 'UNCLASSIFIED',
      policy: { entityTypes: ['AIRCRAFT'], boxes: [{ north: 10, south: 0, east: 10, west: 0 }] },
    });
    req.flush({
      error: 'validation failed',
      details: [
        { path: '/config/policy/boxes/0/south', message: 'outside the allowed area' },
        { path: '/config/classification', message: 'above your clearance' },
      ],
    }, { status: 422, statusText: 'Unprocessable Entity' });
    fixture.detectChanges();

    expect(component.policyProblems()).toEqual([{ path: '/config/policy/boxes/0/south', message: 'outside the allowed area' }]);
    expect(component.schemaProblems()).toEqual([]);
    expect(el.querySelector('app-policy-editor [data-pointer="/config/policy/boxes/0/south"]')?.classList.contains('has-issue')).toBe(true);
    expect(el.querySelector('app-policy-editor [data-pointer="/config/policy/boxes/0"] .pe-error-server')?.textContent).toContain('outside the allowed area');
    expect(el.querySelector('[data-pointer="/config/classification"] .error')?.textContent).toContain('above your clearance');
    expect(el.querySelector('app-schema-form .sf-unmatched')).toBeNull();
  });

  it('refuses to submit while the policy is invalid', () => {
    open('create');
    component.selectDriver('kml-push');
    fixture.detectChanges();
    type('#feed-name', 'KML');

    Array.from(el.querySelectorAll<HTMLButtonElement>('app-policy-editor button')).find((b) => b.textContent?.trim() === 'Add area')!.click();
    fixture.detectChanges();
    expect(component.canSubmit()).toBe(false);

    component.submit();
    fixture.detectChanges();
    httpMock.expectNone('/api/v1/feeds');
    expect(el.querySelector('.form-error')?.textContent).toContain('Fix the highlighted fields');
    expect(el.querySelector('app-policy-editor .pe-error')?.textContent).toContain('is required');
  });

  it('lets a both-way driver be narrowed, hiding the policy when it only receives', () => {
    open('create');
    component.selectDriver('tak');
    fixture.detectChanges();

    expect(el.querySelector('#feed-direction')).toBeTruthy();
    expect(el.querySelector('app-policy-editor')).toBeTruthy();
    expect(component.effectiveDirection()).toBe('both');

    component.form.controls.direction.setValue('in');
    fixture.detectChanges();
    expect(component.effectiveDirection()).toBe('in');
    expect(el.querySelector('app-policy-editor')).toBeNull();
    expect(component.externalKeys()).toContain('outbound');

    type('#feed-name', 'TAK HQ');
    type('app-schema-form input[name="host"]', 'tak.example.org');
    component.submit();
    const body = httpMock.expectOne('/api/v1/feeds').request.body;
    expect(body.config).toEqual({ host: 'tak.example.org', classification: 'UNCLASSIFIED', direction: 'in' });
    expect('policy' in body.config).toBe(false);
  });

  it('edit: prefills the policy and direction of a sending connection', () => {
    const sink: Feed = {
      ...existing,
      id: 'tak-hq',
      driver: 'tak',
      direction: 'out',
      config: {
        host: 'tak.example.org',
        classification: 'SECRET',
        direction: 'out',
        policy: { entityTypes: ['VESSEL'], classificationCeiling: 'CONFIDENTIAL', reexport: true },
      },
      secretRefs: {},
    };
    open('edit', sink);

    expect(el.querySelector<HTMLSelectElement>('#feed-direction')?.value).toBe('out');
    expect(el.querySelector<HTMLSelectElement>('#feed-classification')?.value).toBe('SECRET');
    const editor = el.querySelector('app-policy-editor')!;
    expect(editor.querySelector<HTMLInputElement>('input[name="entityType"][value="VESSEL"]')?.checked).toBe(true);
    expect(editor.querySelector<HTMLSelectElement>('#pe-ceiling')?.value).toBe('CONFIDENTIAL');
    expect(editor.querySelector<HTMLInputElement>('input[name="reexport"]')?.checked).toBe(true);
    expect(el.querySelector('app-schema-form select[name="direction"]')).toBeNull();

    component.submit();
    const req = httpMock.expectOne('/api/v1/feeds/tak-hq');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body.config).toEqual({
      host: 'tak.example.org',
      classification: 'SECRET',
      direction: 'out',
      policy: { entityTypes: ['VESSEL'], classificationCeiling: 'CONFIDENTIAL', reexport: true },
    });
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
