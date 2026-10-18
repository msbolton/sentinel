import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ConnectionFormComponent, slugify } from './connection-form.component';
import { AuthService } from '../../core/services/auth.service';
import { FeedService } from '../../core/services/feed.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { Feed, FeedDriver, ListenPorts } from '../../shared/models/feed.model';

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
      allow: { type: 'array', items: { type: 'string' }, title: 'Allowed senders', 'x-showWhen': { mode: 'listen' } },
      tls: {
        type: 'object',
        title: 'TLS',
        properties: {
          enabled: { type: 'boolean', default: false, title: 'Use TLS' },
          requireClientCert: {
            type: 'boolean',
            default: false,
            title: 'Require client certificates',
            'x-showWhen': { enabled: true },
          },
        },
      },
    },
  },
  secretFields: [],
};

/** The UDP driver, trimmed the same way: it always listens. */
const udpDriver: FeedDriver = {
  id: 'udp',
  name: 'UDP',
  description: 'An external system sends datagrams',
  source: '',
  direction: 'in',
  capabilities: ['listen'],
  configSchema: {
    type: 'object',
    required: ['port'],
    properties: {
      port: { type: 'integer', minimum: 1024, maximum: 65535, title: 'Port' },
      allow: { type: 'array', items: { type: 'string' }, title: 'Allowed senders' },
    },
  },
  secretFields: [],
};

/** A stream whose transport can listen (tcpserver, udp) or dial out (tcpclient); the port is part of its address. */
const streamListenerDriver: FeedDriver = {
  id: 'stream',
  name: 'Message stream',
  description: 'Bytes over a transport',
  source: 'ADS_B',
  capabilities: [],
  configSchema: {
    type: 'object',
    required: ['transport'],
    properties: {
      transport: {
        type: 'object',
        title: 'Transport',
        required: ['kind'],
        properties: {
          kind: { type: 'string', enum: ['tcpserver', 'udp', 'tcpclient'], title: 'Kind' },
          address: { type: 'string', title: 'Address' },
          allow: {
            type: 'array',
            items: { type: 'string' },
            title: 'Allowed senders',
            'x-showWhen': { kind: ['tcpserver', 'udp'] },
          },
        },
      },
    },
  },
  secretFields: [],
};

/** What `GET /api/v1/feeds/ports` answers: Sentinel's own ports, what connections hold, a suggestion per protocol. */
const portsBody: ListenPorts = {
  min: 1024,
  max: 65535,
  reserved: [{ from: 3000, to: 3005 }],
  taken: [
    { protocol: 'tcp', port: 6000, feedId: 'radar', feedName: 'Radar feed', enabled: true },
    { protocol: 'udp', port: 10110, feedId: 'pier', feedName: 'Pier AIS', enabled: false },
  ],
  suggested: { tcp: 6001, udp: 10111 },
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

  /** Loads the catalog again with these drivers in it, replacing any of the same id. */
  function addDrivers(...extra: FeedDriver[]): void {
    TestBed.inject(FeedService).loadDrivers().subscribe();
    httpMock.expectOne('/api/v1/drivers').flush([...drivers.filter((d) => !extra.some((e) => e.id === d.id)), ...extra]);
  }

  /** Answers the form's request for the ports in use. */
  function flushPorts(body: ListenPorts = portsBody): void {
    const req = httpMock.expectOne('/api/v1/feeds/ports');
    expect(req.request.method).toBe('GET');
    req.flush(body);
    fixture.detectChanges();
  }

  /** A stored TCP connection, as the edit form is opened on it. */
  function tcpFeed(config: Record<string, unknown>, id = 'radar', name = 'Radar feed'): Feed {
    return { ...existing, id, name, driver: 'tcp', source: '', enabled: true, config, secretRefs: {} };
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
    flushPorts();

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

  // A connection that listens is told, while it is edited, about the port it picks and the senders it lets in.
  describe('a listening connection', () => {
    beforeEach(() => addDrivers(tcpDriver, udpDriver, streamListenerDriver));

    /** The hint shown with the field at `pointer`. */
    function hintAt(pointer: string): HTMLElement | null {
      return el.querySelector<HTMLElement>(`[data-pointer="${pointer}"] > .sf-hint`);
    }

    function text(node: Element | null): string {
      return node?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
    }

    /** Takes what the hint at `pointer` offers. */
    function use(pointer: string): void {
      const button = hintAt(pointer)?.querySelector('button');
      if (!button) {
        throw new Error(`no action on the hint at ${pointer}`);
      }
      button.click();
      fixture.detectChanges();
    }

    function setConfig(pointer: string, value: unknown): void {
      component.schemaForm()!.control(pointer)!.setValue(value);
      fixture.detectChanges();
    }

    const portInput = 'app-schema-form input[name="port"]';

    it('create: suggests a free port and flags a taken one for a TCP listener', () => {
      open('create');
      component.selectDriver('tcp');
      fixture.detectChanges();
      flushPorts();

      expect(text(hintAt('/config/port'))).toBe('Port 6001 is not used by Sentinel or another TCP connection. Use 6001');
      expect(hintAt('/config/port')?.classList.contains('sf-hint-warn')).toBe(false);
      // a screen reader reads it with the port field
      const port = el.querySelector<HTMLInputElement>(portInput)!;
      expect(port.getAttribute('aria-describedby')?.split(' ')).toContain(hintAt('/config/port')!.id);

      hintAt('/config/port')!.querySelector('button')!.focus();
      use('/config/port');
      expect(component.schemaForm()?.control('/config/port')?.value).toBe(6001);
      expect(port.value).toBe('6001');
      // a free port needs no remark, and the button that is gone does not take the focus with it
      expect(hintAt('/config/port')).toBeNull();
      expect(document.activeElement).toBe(port);
      expect(port.hasAttribute('aria-describedby')).toBe(false);

      // a port another TCP connection holds
      type(portInput, '6000');
      expect(text(hintAt('/config/port'))).toBe('Warning: Port 6000/tcp is already used by Radar feed (radar). Use 6001');
      expect(hintAt('/config/port')?.classList.contains('sf-hint-warn')).toBe(true);
      use('/config/port');
      expect(component.schemaForm()?.control('/config/port')?.value).toBe(6001);

      // one of Sentinel's own
      type(portInput, '3002');
      expect(text(hintAt('/config/port'))).toBe(
        "Warning: Port 3002 is one of Sentinel's own (INGEST_RESERVED_PORTS). Use 3006",
      );
      use('/config/port');
      expect(el.querySelector<HTMLInputElement>(portInput)?.value).toBe('3006');

      // a warning is no obstacle: the connection can be saved with the port it flags
      type(portInput, '6000');
      type('#feed-name', 'Radar feed');
      expect(hintAt('/config/port')).not.toBeNull();
      expect(component.canSubmit()).toBe(true);
    });

    it('warns while the allowlist is empty', () => {
      open('create');
      component.selectDriver('tcp');
      fixture.detectChanges();
      flushPorts();
      type(portInput, '6100');

      const warning = () => hintAt('/config/allow');
      expect(text(warning())).toBe(
        "Warning: Empty: anyone who can reach port 6100 can connect. List the senders' IP addresses or CIDR ranges.",
      );
      expect(warning()?.classList.contains('sf-hint-warn')).toBe(true);
      expect(warning()?.querySelector('button')).toBeNull();

      type('app-schema-form input[name="allow"]', '10.0.0.0/8, 192.0.2.7');
      expect(warning()).toBeNull();
      type('app-schema-form input[name="allow"]', '');
      expect(warning()).not.toBeNull();

      // connecting dials out: nothing listens, so nothing is left open
      setConfig('/config/mode', 'connect');
      expect(el.querySelector('app-schema-form .sf-hint')).toBeNull();
    });

    it('treats an open allowlist as information once TLS asks the senders for certificates', () => {
      open('create');
      component.selectDriver('tcp');
      fixture.detectChanges();
      flushPorts();
      type(portInput, '6100');
      const note = () => hintAt('/config/allow');
      expect(note()?.classList.contains('sf-hint-warn')).toBe(true);

      // TLS alone proves the listener to the sender, not the sender to the listener
      setConfig('/config/tls/enabled', true);
      expect(note()?.classList.contains('sf-hint-warn')).toBe(true);

      setConfig('/config/tls/requireClientCert', true);
      expect(note()?.classList.contains('sf-hint-warn')).toBe(false);
      expect(text(note())).toBe(
        'Empty: any address can attempt a connection, but only senders with a certificate signed by the client CA get in.',
      );

      setConfig('/config/tls/requireClientCert', false);
      expect(note()?.classList.contains('sf-hint-warn')).toBe(true);
    });

    it('udp: asks at once, suggests a UDP port and warns that senders can be forged', () => {
      open('create');
      component.selectDriver('udp');
      fixture.detectChanges();
      flushPorts();

      expect(text(hintAt('/config/port'))).toBe('Port 10111 is not used by Sentinel or another UDP connection. Use 10111');
      expect(text(hintAt('/config/allow'))).toBe(
        "Warning: Empty: anyone who can reach the port can connect. List the senders' IP addresses or CIDR ranges. " +
          'UDP source addresses can be forged; on an untrusted network prefer TCP with mutual TLS.',
      );

      use('/config/port');
      expect(component.schemaForm()?.control('/config/port')?.value).toBe(10111);
      // a port a TCP connection holds is free on UDP
      type(portInput, '6000');
      expect(hintAt('/config/port')).toBeNull();
      // a disabled UDP connection keeps its port
      type(portInput, '10110');
      expect(text(hintAt('/config/port'))).toBe(
        'Warning: Port 10110/udp is already used by Pier AIS (pier). Disabled connections keep their port. Use 10111',
      );
    });

    it('stream: offers an address once the transport listens', () => {
      open('create');
      component.selectDriver('stream');
      fixture.detectChanges();
      // no transport yet, so nothing is bound
      httpMock.expectNone('/api/v1/feeds/ports');

      setConfig('/config/transport/kind', 'tcpserver');
      flushPorts();
      const address = 'app-schema-form input[name="address"]';

      expect(text(hintAt('/config/transport/address'))).toBe(
        'Port 6001 is not used by Sentinel or another TCP connection. Use 6001',
      );
      use('/config/transport/address');
      expect(el.querySelector<HTMLInputElement>(address)?.value).toBe(':6001');

      // the interface stays, only the port changes
      type(address, '10.0.0.5:6000');
      expect(text(hintAt('/config/transport/address'))).toContain('Port 6000/tcp is already used by Radar feed (radar).');
      use('/config/transport/address');
      expect(el.querySelector<HTMLInputElement>(address)?.value).toBe('10.0.0.5:6001');

      // the address is free text, so the floor is pointed out here
      type(address, ':80');
      expect(text(hintAt('/config/transport/address'))).toBe('Warning: Listening ports start at 1024.');

      // the allowlist is inside the transport
      type(address, ':6100');
      expect(text(hintAt('/config/transport/allow'))).toContain('Empty: anyone who can reach port 6100 can connect.');
      expect(hintAt('/config/transport/address')).toBeNull();

      // dialing out binds nothing
      setConfig('/config/transport/kind', 'tcpclient');
      expect(el.querySelector('app-schema-form .sf-hint')).toBeNull();
    });

    it('never asks for ports for a driver that cannot listen', () => {
      open('create');
      for (const id of ['opensky', 'tak', 'kml-push']) {
        component.selectDriver(id);
        fixture.detectChanges();
        httpMock.expectNone('/api/v1/feeds/ports');
        expect(el.querySelector('app-schema-form .sf-hint')).toBeNull();
        component.back();
        fixture.detectChanges();
      }

      // a stream that has not picked a transport binds nothing, nor does one that dials out
      component.selectDriver('stream');
      fixture.detectChanges();
      httpMock.expectNone('/api/v1/feeds/ports');
      setConfig('/config/transport/kind', 'tcpclient');
      httpMock.expectNone('/api/v1/feeds/ports');

      // nor does a TCP connection that connects
      open('edit', tcpFeed({ mode: 'connect', host: 'radar.example', port: 7000 }));
      httpMock.expectNone('/api/v1/feeds/ports');
      expect(el.querySelector('app-schema-form .sf-hint')).toBeNull();
    });

    it('asks when a connection starts to listen, and once for each time the form is opened', () => {
      open('edit', tcpFeed({ mode: 'connect', host: 'radar.example', port: 7000 }));
      httpMock.expectNone('/api/v1/feeds/ports');

      setConfig('/config/mode', 'listen');
      flushPorts();
      setConfig('/config/mode', 'connect');
      setConfig('/config/mode', 'listen');
      setConfig('/config/port', 7001);
      httpMock.expectNone('/api/v1/feeds/ports');

      // another connection is another opening, and the answer may have changed
      open('edit', tcpFeed({ mode: 'listen', port: 7002 }, 'gate', 'Gate feed'));
      flushPorts();
    });

    // The drawer can be walked back to the list of drivers while the question about the ports is still out.
    describe('when the driver changes before the ports are answered', () => {
      function pickTcpThenGoBack(): void {
        open('create');
        component.selectDriver('tcp');
        fixture.detectChanges();
        component.back();
        fixture.detectChanges();
      }

      it('asks once, and the next driver\'s hints come from that answer', () => {
        pickTcpThenGoBack();
        component.selectDriver('udp');
        fixture.detectChanges();

        const requests = httpMock.match('/api/v1/feeds/ports');
        expect(requests).toHaveLength(1);
        requests[0].flush(portsBody);
        fixture.detectChanges();

        expect(text(hintAt('/config/port'))).toBe('Port 10111 is not used by Sentinel or another UDP connection. Use 10111');
        expect(text(hintAt('/config/allow'))).toContain('UDP source addresses can be forged');
        httpMock.expectNone('/api/v1/feeds/ports');
      });

      it('shows no hint for a driver that cannot listen', () => {
        pickTcpThenGoBack();
        component.selectDriver('opensky');
        fixture.detectChanges();

        const requests = httpMock.match('/api/v1/feeds/ports');
        expect(requests).toHaveLength(1);
        requests[0].flush(portsBody);
        fixture.detectChanges();

        expect(el.querySelector('app-schema-form .sf-hint')).toBeNull();
        // and coming back to a listener uses the answer it already has
        component.back();
        fixture.detectChanges();
        component.selectDriver('udp');
        fixture.detectChanges();
        httpMock.expectNone('/api/v1/feeds/ports');
        expect(text(hintAt('/config/port'))).toContain('Port 10111 is not used by Sentinel or another UDP connection.');
      });
    });

    it('stops waiting for the ports when the form closes', () => {
      open('create');
      component.selectDriver('tcp');
      fixture.detectChanges();
      const req = httpMock.expectOne('/api/v1/feeds/ports');

      fixture.destroy();

      expect(req.cancelled).toBe(true);
    });

    it('edit: leaves the connection its own port, and flags it for another connection', () => {
      const allow = ['10.0.0.0/8'];
      open('edit', tcpFeed({ mode: 'listen', port: 6000, allow }, 'radar'));
      flushPorts();
      expect(hintAt('/config/port')).toBeNull();
      expect(hintAt('/config/allow')).toBeNull();

      open('edit', tcpFeed({ mode: 'listen', port: 6000, allow }, 'gate', 'Gate feed'));
      flushPorts();
      expect(text(hintAt('/config/port'))).toBe('Warning: Port 6000/tcp is already used by Radar feed (radar). Use 6001');
    });

    it('still saves when the ports endpoint is missing', () => {
      open('create');
      component.selectDriver('tcp');
      fixture.detectChanges();
      httpMock.expectOne('/api/v1/feeds/ports').flush({ error: 'not found' }, { status: 404, statusText: 'Not Found' });
      fixture.detectChanges();

      // no port hints, no complaint; the allowlist warning does not depend on the ports
      expect(hintAt('/config/port')).toBeNull();
      expect(el.querySelector('.sf-hint-action')).toBeNull();
      expect(el.querySelector('.form-error')).toBeNull();
      expect(text(hintAt('/config/allow'))).toContain('Empty: anyone who can reach the port can connect.');

      type('#feed-name', 'Radar feed');
      type(portInput, '6100');
      expect(component.canSubmit()).toBe(true);
      component.submit();

      const save = httpMock.expectOne('/api/v1/feeds');
      expect(save.request.method).toBe('POST');
      expect(save.request.body.config).toEqual({
        mode: 'listen',
        port: 6100,
        tls: { enabled: false },
        classification: 'UNCLASSIFIED',
      });
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
