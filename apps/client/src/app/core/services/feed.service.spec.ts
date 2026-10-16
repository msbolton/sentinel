import { TestBed, fakeAsync, tick } from '@angular/core/testing';
import { HttpErrorResponse, provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { FeedService, problemsOf } from './feed.service';
import { Feed, FeedStatus } from '../../shared/models/feed.model';

function makeStatus(overrides: Partial<FeedStatus> = {}): FeedStatus {
  return {
    owner: 'ingest-0',
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
    ...overrides,
  };
}

function makeFeed(id: string, overrides: Partial<Feed> = {}): Feed {
  return {
    id,
    name: id,
    description: '',
    driver: 'opensky',
    source: 'OPENSKY',
    builtin: true,
    enabled: true,
    version: 1,
    config: {},
    secretRefs: {},
    capabilities: [],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    status: makeStatus(),
    ...overrides,
  };
}

describe('FeedService', () => {
  let service: FeedService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    service = TestBed.inject(FeedService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
    TestBed.resetTestingModule();
  });

  it('load() populates the feeds signal and marks the service loaded', () => {
    expect(service.hasLoaded).toBe(false);
    service.load().subscribe();
    expect(service.loading()).toBe(true);

    const req = http.expectOne('/api/v1/feeds');
    expect(req.request.method).toBe('GET');
    req.flush([makeFeed('opensky'), makeFeed('ais-demo', { builtin: false, enabled: false })]);

    expect(service.hasLoaded).toBe(true);
    expect(service.loading()).toBe(false);
    expect(service.error()).toBeNull();
    expect(service.feeds().map((f) => f.id)).toEqual(['opensky', 'ais-demo']);
    expect(service.enabledFeeds().map((f) => f.id)).toEqual(['opensky']);
    expect(service.feed('ais-demo')?.enabled).toBe(false);
  });

  it('load() records the server message after retries are exhausted', fakeAsync(() => {
    let failed = false;
    service.load().subscribe({ error: () => (failed = true) });

    for (let attempt = 0; attempt < 4; attempt++) {
      http.expectOne('/api/v1/feeds').flush({ error: 'ingest offline' }, { status: 503, statusText: 'Unavailable' });
      tick(attempt * 2000 + 2000);
    }

    expect(failed).toBe(true);
    expect(service.hasLoaded).toBe(false);
    expect(service.loading()).toBe(false);
    expect(service.error()).toBe('ingest offline');
  }));

  it('loadDrivers() populates the drivers signal', () => {
    service.loadDrivers().subscribe();
    http.expectOne('/api/v1/drivers').flush([
      { id: 'opensky', name: 'OpenSky', description: '', source: 'OPENSKY', capabilities: [], configSchema: {}, secretFields: [] },
    ]);
    expect(service.drivers().map((d) => d.id)).toEqual(['opensky']);
    expect(service.driver('opensky')?.name).toBe('OpenSky');
  });

  it('setEnabled() posts to enable/disable and updates the feed in place', () => {
    service.load().subscribe();
    http.expectOne('/api/v1/feeds').flush([makeFeed('opensky', { enabled: false }), makeFeed('celestrak')]);

    service.setEnabled('opensky', true).subscribe();
    const enable = http.expectOne('/api/v1/feeds/opensky/enable');
    expect(enable.request.method).toBe('POST');
    enable.flush(makeFeed('opensky', { enabled: true, version: 2 }));
    expect(service.feed('opensky')?.enabled).toBe(true);
    expect(service.feed('opensky')?.version).toBe(2);
    expect(service.feeds().map((f) => f.id)).toEqual(['opensky', 'celestrak']);

    service.setEnabled('celestrak', false).subscribe();
    const disable = http.expectOne('/api/v1/feeds/celestrak/disable');
    expect(disable.request.method).toBe('POST');
    disable.flush(makeFeed('celestrak', { enabled: false }));
    expect(service.feed('celestrak')?.enabled).toBe(false);
    expect(service.enabledFeeds().map((f) => f.id)).toEqual(['opensky']);
  });

  it('create() posts the body and appends the created feed', () => {
    service.load().subscribe();
    http.expectOne('/api/v1/feeds').flush([makeFeed('opensky')]);

    const body = { name: 'Harbour AIS', driver: 'ais-tcp', config: { host: 'ais.local' } };
    service.create(body).subscribe();
    const req = http.expectOne('/api/v1/feeds');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual(body);
    req.flush(makeFeed('harbour-ais', { name: 'Harbour AIS', driver: 'ais-tcp', builtin: false }));

    expect(service.feeds().map((f) => f.id)).toEqual(['opensky', 'harbour-ais']);
  });

  it('replace() puts the body and swaps the feed in place', () => {
    service.load().subscribe();
    http.expectOne('/api/v1/feeds').flush([makeFeed('opensky'), makeFeed('harbour-ais', { builtin: false })]);

    service.replace('harbour-ais', { name: 'Harbour AIS v2', config: {}, secretRefs: {}, enabled: true }).subscribe();
    const req = http.expectOne('/api/v1/feeds/harbour-ais');
    expect(req.request.method).toBe('PUT');
    req.flush(makeFeed('harbour-ais', { name: 'Harbour AIS v2', builtin: false, version: 2 }));

    expect(service.feeds().map((f) => f.name)).toEqual(['opensky', 'Harbour AIS v2']);
  });

  it('delete() removes the feed from the signal', () => {
    service.load().subscribe();
    http.expectOne('/api/v1/feeds').flush([makeFeed('opensky'), makeFeed('harbour-ais', { builtin: false })]);

    service.delete('harbour-ais').subscribe();
    const req = http.expectOne('/api/v1/feeds/harbour-ais');
    expect(req.request.method).toBe('DELETE');
    req.flush(null, { status: 204, statusText: 'No Content' });

    expect(service.feeds().map((f) => f.id)).toEqual(['opensky']);
  });

  it('poll() fetches immediately, then on the interval, until the last release', fakeAsync(() => {
    const releaseA = service.poll(15_000);
    http.expectOne('/api/v1/feeds').flush([makeFeed('opensky')]);
    expect(service.feeds().length).toBe(1);

    // A second view sharing the poller does not start another one.
    const releaseB = service.poll(15_000);
    http.expectNone('/api/v1/feeds');

    tick(15_000);
    http.expectOne('/api/v1/feeds').flush([makeFeed('opensky'), makeFeed('celestrak')]);
    expect(service.feeds().length).toBe(2);

    releaseA();
    tick(15_000);
    http.expectOne('/api/v1/feeds').flush([]);

    releaseB();
    releaseB(); // double release is harmless
    tick(45_000);
    http.expectNone('/api/v1/feeds');
  }));

  it('poll() survives a failing fetch and keeps polling', fakeAsync(() => {
    const release = service.poll(30_000);
    // retry(3) with 2s, 4s, 6s backoff: four attempts (12s) before the error surfaces.
    for (let attempt = 0; attempt < 3; attempt++) {
      http.expectOne('/api/v1/feeds').flush('down', { status: 503, statusText: 'Unavailable' });
      tick((attempt + 1) * 2000);
    }
    http.expectOne('/api/v1/feeds').flush('down', { status: 503, statusText: 'Unavailable' });
    expect(service.feeds()).toEqual([]);
    expect(service.error()).not.toBeNull();

    // Nothing more until the next scheduled poll at 30s.
    tick(17_000);
    http.expectNone('/api/v1/feeds');
    tick(1_000);
    http.expectOne('/api/v1/feeds').flush([makeFeed('opensky')]);
    expect(service.feeds().length).toBe(1);
    expect(service.error()).toBeNull();

    release();
    tick(60_000);
    http.expectNone('/api/v1/feeds');
  }));
});

describe('problemsOf', () => {
  it('extracts the message and validation details from a 422 body', () => {
    const err = new HttpErrorResponse({
      status: 422,
      statusText: 'Unprocessable Entity',
      url: '/api/v1/feeds',
      error: {
        error: 'invalid feed config',
        details: [
          { path: 'config.host', message: 'is required' },
          { path: 'secretRefs.password', message: 'must be a secret reference' },
        ],
      },
    });

    expect(problemsOf(err)).toEqual({
      message: 'invalid feed config',
      problems: [
        { path: 'config.host', message: 'is required' },
        { path: 'secretRefs.password', message: 'must be a secret reference' },
      ],
    });
  });

  it('falls back to the HTTP message when the body is not a feed error', () => {
    const err = new HttpErrorResponse({ status: 500, statusText: 'Server Error', url: '/api/v1/feeds', error: 'boom' });
    const result = problemsOf(err);
    expect(result.problems).toEqual([]);
    expect(result.message).toContain('500');
  });

  it('stringifies non-HTTP errors', () => {
    expect(problemsOf(new Error('offline'))).toEqual({ message: 'Error: offline', problems: [] });
  });
});
