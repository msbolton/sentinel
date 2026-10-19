import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { BehaviorSubject } from 'rxjs';
import { ConnectionsComponent } from './connections.component';
import { AuthService, UserProfile } from '../../core/services/auth.service';
import { FeedService } from '../../core/services/feed.service';
import { Feed, FeedDriver } from '../../shared/models/feed.model';

function makeFeed(over: Partial<Feed> & { id: string }): Feed {
  return {
    name: over.id,
    description: '',
    driver: 'opensky',
    source: 'OPENSKY',
    builtin: false,
    enabled: true,
    version: 1,
    config: {},
    secretRefs: {},
    capabilities: ['viewport'],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    status: {
      owner: 'ingest-0',
      state: 'running',
      health: 'healthy',
      lastMessageAt: new Date(Date.now() - 15_000).toISOString(),
      lastSuccessAt: null,
      messagesTotal: 1234,
      entitiesLast: 56,
      errorsTotal: 0,
      appliedVersion: 1,
      detail: { creditsRemaining: 3900, idle: true },
      updatedAt: null,
    },
    ...over,
  };
}

const drivers: FeedDriver[] = [
  {
    id: 'opensky',
    name: 'OpenSky Network',
    description: 'ADS-B states',
    source: 'OPENSKY',
    capabilities: ['viewport'],
    configSchema: { type: 'object', properties: { intervalSec: { type: 'integer', minimum: 1 } } },
    secretFields: ['clientId', 'clientSecret'],
  },
];

describe('ConnectionsComponent', () => {
  let fixture: ComponentFixture<ConnectionsComponent>;
  let component: ConnectionsComponent;
  let el: HTMLElement;
  let httpMock: HttpTestingController;
  let profile$: BehaviorSubject<UserProfile | null>;

  /** Signs in with these roles (the page opens for an operator). */
  const signInAs = (...roles: string[]) => profile$.next({ username: 'tester', roles, classificationLevel: 'UNCLASSIFIED' });

  function flushInitial(feeds: Feed[]): void {
    httpMock.expectOne('/api/v1/feeds').flush(feeds);
    httpMock.expectOne('/api/v1/drivers').flush(drivers);
    fixture.detectChanges();
  }

  function rowOf(id: string): HTMLTableRowElement {
    const rows = Array.from(el.querySelectorAll<HTMLTableRowElement>('tr.feed-row'));
    const row = rows.find((r) => r.querySelector('.feed-id')?.textContent?.trim() === id);
    if (!row) {
      throw new Error(`no row for ${id}`);
    }
    return row;
  }

  beforeEach(async () => {
    profile$ = new BehaviorSubject<UserProfile | null>({ username: 'op', roles: ['sentinel-operator'], classificationLevel: 'UNCLASSIFIED' });
    await TestBed.configureTestingModule({
      imports: [ConnectionsComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { getClassificationLevel: () => 'UNCLASSIFIED', userProfile$: profile$ } },
      ],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(ConnectionsComponent);
    component = fixture.componentInstance;
    el = fixture.nativeElement as HTMLElement;
    fixture.detectChanges();
  });

  afterEach(() => {
    fixture.destroy();
    httpMock.verify();
  });

  it('lists feeds with driver name, state, health, counters and relative time', () => {
    flushInitial([
      makeFeed({ id: 'opensky-global', name: 'OpenSky global', builtin: true, description: 'Everything' }),
      makeFeed({
        id: 'custom',
        enabled: false,
        status: {
          ...makeFeed({ id: 'x' }).status,
          state: 'error',
          health: 'critical',
          lastError: 'boom',
          errorsTotal: 3,
          lastMessageAt: null,
          detail: {},
        },
      }),
    ]);

    const builtin = rowOf('opensky-global');
    expect(builtin.querySelector('.feed-name')?.textContent).toContain('OpenSky global');
    expect(builtin.querySelector('.tag')?.textContent?.trim()).toBe('built-in');
    expect(builtin.querySelector('.feed-description')?.textContent?.trim()).toBe('Everything');
    expect(builtin.cells[2].textContent?.trim()).toBe('OpenSky Network');
    expect(builtin.querySelector('.direction-badge')?.textContent?.trim()).toBe('in');
    expect(builtin.cells[4].textContent?.trim()).toBe('OPENSKY');
    expect(builtin.cells[5].textContent?.trim()).toBe('ingest-0');
    expect(builtin.querySelector('.state-badge')?.textContent?.trim()).toBe('Running');
    expect(builtin.querySelector('.health-dot')?.classList.contains('health-healthy')).toBe(true);
    expect(builtin.querySelector('.highlights')?.textContent?.trim()).toBe('idle · 3900 credits');
    expect(builtin.cells[7].textContent?.trim()).toMatch(/^1[0-9]s ago$/);
    expect(builtin.querySelector('.col-counters')?.textContent?.replace(/\s+/g, '')).toBe('1,234/56/0');
    expect(builtin.querySelector<HTMLInputElement>('.switch input')?.checked).toBe(true);

    const deleteBtn = builtin.querySelector<HTMLButtonElement>('.btn-delete');
    expect(deleteBtn?.disabled).toBe(true);
    expect(deleteBtn?.parentElement?.getAttribute('title')).toBe('Built-in connections cannot be deleted');

    const custom = rowOf('custom');
    expect(custom.querySelector('.state-badge')?.textContent?.trim()).toBe('Error');
    expect(custom.querySelector('.health-dot')?.classList.contains('health-critical')).toBe(true);
    expect(custom.querySelector('.last-error')?.textContent?.trim()).toBe('boom');
    expect(custom.cells[7].textContent?.trim()).toBe('never');
    expect(custom.querySelector('.errors')?.textContent?.trim()).toBe('3');
    expect(custom.querySelector<HTMLInputElement>('.switch input')?.checked).toBe(false);
    expect(custom.querySelector<HTMLButtonElement>('.btn-delete')?.disabled).toBe(false);
  });

  it('shows a sending connection with its policy, delivery counters and sink rows', () => {
    flushInitial([
      makeFeed({
        id: 'tak-hq',
        name: 'TAK HQ',
        driver: 'tak',
        source: 'COT',
        direction: 'both',
        config: {
          classification: 'SECRET',
          policy: {
            entityTypes: ['AIRCRAFT', 'VESSEL'],
            boxes: [{ north: 1, south: 0, east: 1, west: 0 }, { north: 3, south: 2, east: 3, west: 2 }],
            classificationCeiling: 'SECRET',
          },
        },
        status: {
          ...makeFeed({ id: 'x' }).status,
          messagesTotal: 900,
          detail: {
            direction: 'both',
            remotePeer: 'tak.example.org:8089',
            tls: true,
            protocol: 'cot-tls',
            outbound: {
              delivered: 1500,
              deliveryErrors: 2,
              denied: 7,
              coalesced: 10,
              dropped: 0,
              queueDepth: 3,
              tracked: 42,
              consumerGroup: 'sink-tak-hq',
              lastDeliveredAt: new Date(Date.now() - 5_000).toISOString(),
            },
          },
        },
      }),
      makeFeed({ id: 'kml', driver: 'kml-push', direction: 'out', config: {} }),
    ]);

    const tak = rowOf('tak-hq');
    expect(tak.querySelector('.direction-badge')?.textContent?.trim()).toBe('both');
    expect(tak.querySelector('.direction-badge')?.classList.contains('direction-both')).toBe(true);
    expect(tak.cells[4].textContent?.trim()).toBe('AIRCRAFT, VESSEL · 2 areas · ≤ SECRET');
    expect(tak.querySelector('.col-counters')?.textContent?.replace(/\s+/g, '')).toBe('1,500/3');
    expect(tak.querySelector('.col-counters span[title="Delivered"]')?.textContent?.trim()).toBe('1,500');
    expect(tak.querySelector('.col-counters span[title="Waiting to go out"]')?.textContent?.trim()).toBe('3');
    expect(tak.querySelector('.highlights')?.textContent?.trim()).toBe('tak.example.org:8089 · tls');

    const kml = rowOf('kml');
    expect(kml.querySelector('.direction-badge')?.textContent?.trim()).toBe('out');
    expect(kml.cells[4].textContent?.trim()).toBe('all types · everywhere · ≤ UNCLASSIFIED');
    expect(kml.querySelector('.col-counters')?.textContent?.replace(/\s+/g, '')).toBe('0/0');

    tak.querySelector<HTMLButtonElement>('.expand-btn')?.click();
    fixture.detectChanges();
    const status = Array.from(el.querySelectorAll('tr.detail-row .detail-block')).find((b) => b.querySelector('h4')?.textContent === 'Status')!;
    const rows = Array.from(status.querySelectorAll('dt')).map((dt) => [dt.textContent?.trim(), dt.nextElementSibling?.textContent?.trim()]);
    expect(rows).toEqual(
      expect.arrayContaining([
        ['Last delivered', expect.stringMatching(/^[0-9]+s ago$/)],
        ['Backlog', '3'],
        ['Tracked', '42'],
        ['Denied by policy', '7'],
        ['Delivery errors', '2'],
        ['Remote peer', 'tak.example.org:8089'],
        ['TLS', 'on'],
      ]),
    );
    expect(el.querySelector('tr.detail-row')?.textContent).not.toContain('leaseToken');
  });

  it('expands a row to show status and driver detail', () => {
    flushInitial([makeFeed({ id: 'a', status: { ...makeFeed({ id: 'a' }).status, lastError: 'rate limited' } })]);

    expect(el.querySelector('tr.detail-row')).toBeNull();
    rowOf('a').querySelector<HTMLButtonElement>('.expand-btn')?.click();
    fixture.detectChanges();

    const detail = el.querySelector('tr.detail-row');
    expect(detail?.textContent).toContain('rate limited');
    expect(detail?.textContent).toContain('creditsRemaining');
    expect(detail?.textContent).toContain('3900');
  });

  it('shows where a TCP/UDP connection listens, under its name and in a send guide', () => {
    flushInitial([
      makeFeed({
        id: 'sensor-udp',
        driver: 'udp',
        config: { port: 10110, encoding: 'json' },
        status: {
          ...makeFeed({ id: 'x' }).status,
          detail: {
            listen: { protocol: 'udp', host: 'h', port: 10110 },
            accepted: 7,
            senders: [{ remote: '10.0.0.5:4000', since: '2026-10-18T10:00:00Z', lastAt: '2026-10-18T10:00:05Z', messages: 3, connected: false }],
          },
        },
      }),
      makeFeed({ id: 'sky' }),
    ]);

    expect(rowOf('sensor-udp').querySelector('.endpoint')?.textContent?.trim()).toBe('h:10110/udp');
    expect(rowOf('sky').querySelector('.endpoint')).toBeNull();

    rowOf('sensor-udp').querySelector<HTMLButtonElement>('.expand-btn')?.click();
    rowOf('sky').querySelector<HTMLButtonElement>('.expand-btn')?.click();
    fixture.detectChanges();

    const guides = Array.from(el.querySelectorAll('tr.detail-row app-send-guide'));
    expect(guides.length).toBe(2);
    const guide = guides[0];
    expect(guide.querySelector('h4')?.textContent).toContain('h:10110/udp');
    expect(guide.querySelector('pre')?.textContent).toContain('nc -u -w1 h 10110');
    expect(Array.from(guide.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toContain(
      'assets/proto/sentinel/v1/observation.proto',
    );
    expect(guide.querySelector('.senders')?.textContent).toContain('10.0.0.5:4000');
    expect(guide.querySelector('.counters dt')?.textContent?.trim()).toBe('Accepted');
    expect(guide.querySelector('.counters dd')?.textContent?.trim()).toBe('7');
    // JSON is tried with netcat; the Go and Python senders write binary protobuf.
    expect(guide.querySelector('details.examples')).toBeNull();
    // Other drivers get no guide.
    expect(guides[1].querySelector('.send-guide')).toBeNull();
  });

  it('guides a listening connection that no listener replica has taken yet', () => {
    flushInitial([
      makeFeed({
        id: 'waiting',
        driver: 'tcp',
        config: { mode: 'listen', port: 6000 },
        status: {
          ...makeFeed({ id: 'x' }).status,
          owner: '',
          state: 'unassigned',
          lastError: 'no listener replica (run the ingest listener: INGEST_LISTENER=true)',
          detail: {},
        },
      }),
    ]);

    expect(rowOf('waiting').querySelector('.endpoint')?.textContent?.trim()).toBe('listener:6000/tcp');
    rowOf('waiting').querySelector<HTMLButtonElement>('.expand-btn')?.click();
    fixture.detectChanges();

    const guide = el.querySelector('tr.detail-row app-send-guide')!;
    expect(guide.querySelector('.warn')?.textContent).toContain('INGEST_LISTENER=true');
    expect(guide.textContent).toContain('No allowlist');
    expect(guide.querySelector('details.examples')?.textContent).toContain('LISTENER_HOST:6000');
    // Nothing reported, so no counters (and no glossary to explain them).
    expect(guide.querySelector('.counters')).toBeNull();
  });

  describe('recent rejects', () => {
    const connections = () => [
      makeFeed({ id: 'sensor-tcp', driver: 'tcp', config: { mode: 'listen', port: 6000 } }),
      makeFeed({ id: 'dialled', driver: 'tcp', config: { mode: 'connect', host: 'h', port: 7000 } }),
      makeFeed({ id: 'sky' }),
    ];
    const expandAll = () => {
      Array.from(el.querySelectorAll<HTMLButtonElement>('.expand-btn')).forEach((b) => b.click());
      fixture.detectChanges();
    };
    const panels = () => Array.from(el.querySelectorAll('tr.detail-row app-dead-letters'));
    /** Opens a panel as a click on its summary does, and waits for the toggle event the browser sends. */
    const open = async (panel: Element) => {
      const details = panel.querySelector('details')!;
      const toggled = new Promise<void>((resolve) => details.addEventListener('toggle', () => resolve(), { once: true }));
      panel.querySelector<HTMLElement>('summary')!.click();
      await toggled;
      fixture.detectChanges();
    };

    it('is offered in every expanded connection, whatever its driver, and asks for nothing until it is opened', () => {
      flushInitial(connections());
      expect(panels()).toHaveLength(0);

      expandAll();
      expect(panels()).toHaveLength(3);
      expect(panels().map((p) => p.querySelector('summary')?.textContent?.trim())).toEqual(Array(3).fill('Recent rejects'));
      expect(panels().every((p) => p.querySelector('details')!.open === false)).toBe(true);
      // closed, so asking nothing: httpMock.verify() in afterEach finds no deadletters request
    });

    it('is hidden for viewers', () => {
      signInAs('sentinel-viewer');
      flushInitial(connections());
      expandAll();

      // the guide is theirs to read; the rejects are not
      expect(el.querySelectorAll('tr.detail-row app-send-guide')).toHaveLength(3);
      expect(panels()).toHaveLength(0);
      expect(el.textContent).not.toContain('Recent rejects');
    });

    it('is shown to admins, and follows the user when the roles change', () => {
      signInAs('sentinel-admin');
      flushInitial(connections());
      expandAll();
      expect(panels()).toHaveLength(3);

      // not an operator or an admin: the analyst and the user with no role see none
      for (const roles of [['sentinel-viewer'], ['sentinel-analyst'], []]) {
        signInAs(...roles);
        fixture.detectChanges();
        expect(panels()).toHaveLength(0);
        signInAs('sentinel-operator');
        fixture.detectChanges();
        expect(panels()).toHaveLength(3);
      }
    });

    it('asks for the rejects of the connection whose row it is in', async () => {
      flushInitial(connections());
      expandAll();

      const dialled = panels()[1];
      await open(dialled);

      httpMock.expectOne('/api/v1/feeds/dialled/deadletters?limit=20').flush({ letters: [], scanned: 10, partial: false });
      fixture.detectChanges();
      expect(dialled.textContent).toContain('No rejects in the last 10 scanned');
      // and the others were not asked
      expect(panels()[0].textContent).not.toContain('scanned');
    });

    it('stays open, with its rejects, when the page refreshes its connections', async () => {
      flushInitial(connections());
      expandAll();
      const panel = panels()[0];
      await open(panel);
      httpMock
        .expectOne('/api/v1/feeds/sensor-tcp/deadletters?limit=20')
        .flush({ letters: [{ at: '2026-10-18T12:00:00Z', reason: 'parse_error', size: 17, partition: 0, offset: 42 }], scanned: 120, partial: false });
      fixture.detectChanges();
      expect(panel.querySelectorAll('tr.reject')).toHaveLength(1);

      // the poll answers again, with every connection a new object
      TestBed.inject(FeedService).load().subscribe();
      httpMock.expectOne('/api/v1/feeds').flush(connections());
      fixture.detectChanges();

      // the same panel, still open, still showing them, and nobody asked the server again
      expect(panels()[0]).toBe(panel);
      expect(panel.querySelector('details')!.open).toBe(true);
      expect(panel.querySelectorAll('tr.reject')).toHaveLength(1);
    });
  });

  describe('listening connections', () => {
    const status = () => makeFeed({ id: 'x' }).status;
    const detail = (d: Record<string, unknown>) => ({ ...status(), detail: d });
    const highlightsOf = (id: string) => rowOf(id).querySelector('.highlights')?.textContent?.trim();
    const idsShown = () => Array.from(el.querySelectorAll('tr.feed-row .feed-id')).map((n) => n.textContent?.trim());
    const chips = () => Array.from(el.querySelectorAll<HTMLButtonElement>('[role=group] .filter-chip'));
    const chip = (name: string) => chips().find((c) => c.textContent?.trim().startsWith(name))!;

    it('a UDP row counts senders, not connections', () => {
      flushInitial([
        makeFeed({ id: 'pier', driver: 'udp', config: { port: 10110 }, status: detail({ transport: 'udp', connections: 0, distinctSenders: 3 }) }),
        makeFeed({ id: 'lone', driver: 'udp', config: { port: 10111 }, status: detail({ transport: 'udp', connections: 0, distinctSenders: 1 }) }),
        makeFeed({ id: 'quiet', driver: 'udp', config: { port: 10112 }, status: detail({ transport: 'udp', connections: 0, distinctSenders: 0 }) }),
        // A stream feed on a udp transport is a datagram listener too.
        makeFeed({ id: 'raw', driver: 'stream', config: { transport: { kind: 'udp', address: ':4001' } }, status: detail({ transport: 'udp', connections: 0, distinctSenders: 2 }) }),
        // Connections are a TCP idea.
        makeFeed({ id: 'plc', driver: 'tcp', config: { mode: 'listen', port: 6000 }, status: detail({ transport: 'tcpserver', connections: 4, distinctSenders: 9 }) }),
        // A datagram listener that has not reported yet says nothing, not "0 conn".
        makeFeed({ id: 'new', driver: 'udp', config: { port: 10113 }, status: detail({ transport: 'udp', connections: 0 }) }),
      ]);

      expect(highlightsOf('pier')).toBe('3 senders');
      expect(highlightsOf('lone')).toBe('1 sender');
      expect(highlightsOf('quiet')).toBe('0 senders');
      expect(highlightsOf('raw')).toBe('2 senders');
      expect(highlightsOf('plc')).toBe('4 conn');
      expect(highlightsOf('new')).toBeUndefined();
    });

    it('shows the format as the source of TCP and UDP connections', () => {
      flushInitial([
        makeFeed({ id: 'ais', driver: 'tcp', source: '', config: { mode: 'listen', port: 6000, format: 'nmea-ais' } }),
        makeFeed({ id: 'plain', driver: 'udp', source: '', config: { port: 10110 } }),
        makeFeed({ id: 'sourced', driver: 'udp', source: '', config: { port: 10111, defaultSource: 'GPS' } }),
        makeFeed({ id: 'dialled', driver: 'tcp', source: '', config: { mode: 'connect', host: 'h', port: 7000, format: 'cot' } }),
        // A default source belongs to Sentinel's own format; a stale one stored beside another format is not shown.
        makeFeed({ id: 'stale', driver: 'tcp', source: '', config: { mode: 'listen', port: 6002, format: 'cot', defaultSource: 'GPS' } }),
        // The server's own source label wins where it has one.
        makeFeed({ id: 'labelled', driver: 'tcp', source: 'ADS_B', config: { mode: 'listen', port: 6001, format: 'sbs' } }),
        makeFeed({ id: 'sky' }),
        makeFeed({ id: 'bare', driver: 'httpjson', source: '', config: {} }),
      ]);

      const source = (id: string) => rowOf(id).cells[4].textContent?.trim();
      expect(source('ais')).toBe('nmea-ais');
      expect(source('plain')).toBe('sentinel');
      expect(source('sourced')).toBe('sentinel · GPS');
      expect(source('dialled')).toBe('cot');
      expect(source('stale')).toBe('cot');
      expect(source('labelled')).toBe('ADS_B');
      expect(source('sky')).toBe('OPENSKY');
      expect(source('bare')).toBe('');
    });

    it('filters to listening connections', () => {
      flushInitial([
        makeFeed({ id: 'tcp-listen', driver: 'tcp', config: { mode: 'listen', port: 6000 } }),
        makeFeed({ id: 'tcp-connect', driver: 'tcp', config: { mode: 'connect', host: 'h', port: 7000 } }),
        makeFeed({ id: 'udp-in', driver: 'udp', config: { port: 10110 } }),
        makeFeed({ id: 'stream-server', driver: 'stream', config: { transport: { kind: 'tcpserver', address: ':4001' } } }),
        makeFeed({ id: 'stream-dial', driver: 'stream', config: { transport: { kind: 'tcpclient', address: 'h:4001' } } }),
        makeFeed({ id: 'sky' }),
      ]);

      // Everything to begin with, and the buttons say how many each shows.
      expect(idsShown()).toEqual(['tcp-listen', 'tcp-connect', 'udp-in', 'stream-server', 'stream-dial', 'sky']);
      expect(chips().map((c) => c.textContent?.replace(/\s+/g, ' ').trim())).toEqual(['All 6', 'Listening 3']);
      expect(chip('All').getAttribute('aria-pressed')).toBe('true');
      expect(chip('Listening').getAttribute('aria-pressed')).toBe('false');
      expect(el.querySelector('[role=group]')?.getAttribute('aria-label')).toBeTruthy();

      chip('Listening').click();
      fixture.detectChanges();
      expect(idsShown()).toEqual(['tcp-listen', 'udp-in', 'stream-server']);
      expect(chip('All').getAttribute('aria-pressed')).toBe('false');
      expect(chip('Listening').getAttribute('aria-pressed')).toBe('true');

      // A row opened while filtered is still there when the filter is lifted.
      rowOf('udp-in').querySelector<HTMLButtonElement>('.expand-btn')?.click();
      chip('All').click();
      fixture.detectChanges();
      expect(idsShown()).toHaveLength(6);
      expect(el.querySelectorAll('tr.detail-row').length).toBe(1);
      expect(chip('All').getAttribute('aria-pressed')).toBe('true');
    });

    it('shows an empty listening view with a way back', () => {
      flushInitial([makeFeed({ id: 'sky' }), makeFeed({ id: 'tcp-connect', driver: 'tcp', config: { mode: 'connect', host: 'h', port: 7000 } })]);

      chip('Listening').click();
      fixture.detectChanges();
      expect(chip('Listening').textContent?.replace(/\s+/g, ' ').trim()).toBe('Listening 0');
      expect(el.querySelector('tr.feed-row')).toBeNull();
      expect(el.querySelector('.empty')?.textContent).toContain('No connection listens on a port.');
      const showAll = Array.from(el.querySelectorAll<HTMLButtonElement>('.empty button')).find((b) => b.textContent?.trim() === 'Show all')!;
      expect(showAll).toBeTruthy();

      showAll.click();
      fixture.detectChanges();
      expect(idsShown()).toEqual(['sky', 'tcp-connect']);
      expect(el.querySelector('.empty')).toBeNull();
      expect(chip('All').getAttribute('aria-pressed')).toBe('true');
    });

    it('offers no filter while there are no connections at all', () => {
      flushInitial([]);
      expect(el.querySelector('.filter-chip')).toBeNull();
      expect(el.querySelector('.empty')?.textContent).toContain('No connections yet');
    });
  });

  it('toggles enabled optimistically and rolls back on error', () => {
    flushInitial([makeFeed({ id: 'a', name: 'Feed A' })]);

    const checkbox = rowOf('a').querySelector<HTMLInputElement>('.switch input')!;
    checkbox.checked = false;
    checkbox.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    expect(rowOf('a').querySelector<HTMLInputElement>('.switch input')?.checked).toBe(false);
    const req = httpMock.expectOne('/api/v1/feeds/a/disable');
    expect(req.request.method).toBe('POST');
    req.flush({ error: 'nope' }, { status: 500, statusText: 'Server Error' });
    fixture.detectChanges();

    expect(rowOf('a').querySelector<HTMLInputElement>('.switch input')?.checked).toBe(true);
    expect(el.querySelector('.banner-error')?.textContent).toContain('Could not disable Feed A: nope');
  });

  it('applies the server response after a successful toggle', () => {
    flushInitial([makeFeed({ id: 'a' })]);

    component.toggleEnabled(component.rows()[0].feed, false);
    httpMock.expectOne('/api/v1/feeds/a/disable').flush(makeFeed({ id: 'a', enabled: false, version: 2 }));
    fixture.detectChanges();

    expect(component.rows()[0].feed.version).toBe(2);
    expect(rowOf('a').querySelector<HTMLInputElement>('.switch input')?.checked).toBe(false);
  });

  it('deletes after an inline confirmation', () => {
    flushInitial([makeFeed({ id: 'a' }), makeFeed({ id: 'b' })]);

    rowOf('a').querySelector<HTMLButtonElement>('.btn-delete')?.click();
    fixture.detectChanges();
    const confirm = rowOf('a').querySelector('.confirm');
    expect(confirm?.textContent).toContain('Delete?');

    // "No" keeps the row
    Array.from(confirm!.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === 'No')?.click();
    fixture.detectChanges();
    expect(rowOf('a').querySelector('.confirm')).toBeNull();

    rowOf('a').querySelector<HTMLButtonElement>('.btn-delete')?.click();
    fixture.detectChanges();
    rowOf('a').querySelector<HTMLButtonElement>('.confirm .btn-danger')?.click();
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/v1/feeds/a');
    expect(req.request.method).toBe('DELETE');
    req.flush(null);
    fixture.detectChanges();

    expect(el.querySelectorAll('tr.feed-row').length).toBe(1);
    expect(() => rowOf('a')).toThrow();
  });

  it('surfaces a refused delete', () => {
    flushInitial([makeFeed({ id: 'a', name: 'Feed A' })]);

    component.askDelete(component.rows()[0].feed);
    component.confirmDelete(component.rows()[0].feed);
    httpMock.expectOne('/api/v1/feeds/a').flush({ error: 'built-in feeds cannot be deleted' }, { status: 403, statusText: 'Forbidden' });
    fixture.detectChanges();

    expect(el.querySelectorAll('tr.feed-row').length).toBe(1);
    expect(el.querySelector('.banner-error')?.textContent).toContain('Could not delete Feed A');
  });

  it('opens the drawer in create and edit mode', () => {
    flushInitial([makeFeed({ id: 'a', name: 'Feed A' })]);
    expect(el.querySelector('app-connection-form')).toBeNull();

    (Array.from(el.querySelectorAll<HTMLButtonElement>('.page-header .btn')).find((b) => b.textContent?.includes('Add connection')))?.click();
    fixture.detectChanges();
    expect(el.querySelector('app-connection-form')).toBeTruthy();
    expect(el.querySelector('app-connection-form h2')?.textContent).toContain('Add connection: choose a driver');

    component.closeDrawer();
    fixture.detectChanges();
    expect(el.querySelector('app-connection-form')).toBeNull();

    Array.from(rowOf('a').querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === 'Edit')?.click();
    fixture.detectChanges();
    expect(el.querySelector('app-connection-form h2')?.textContent).toContain('Edit Feed A');
  });

  it('releases the poll on destroy', () => {
    flushInitial([]);
    fixture.destroy();
    // no further /api/v1/feeds request is pending; verify() in afterEach confirms it
  });
});
