import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ApiKey, ApiKeyService } from './api-key.service';

const key: ApiKey = {
  id: 'k1',
  name: 'QGIS laptop',
  prefix: 'sk_abc1',
  classification: 'UNCLASSIFIED',
  createdAt: '2026-10-01T00:00:00Z',
};

describe('ApiKeyService', () => {
  let service: ApiKeyService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    service = TestBed.inject(ApiKeyService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('starts without knowing whether the gateway serves keys', () => {
    expect(service.available()).toBeNull();
    expect(service.keys()).toEqual([]);
  });

  it('list() fills the keys signal and marks the endpoint available', () => {
    service.list().subscribe();
    const req = http.expectOne('/api/v1/auth/api-keys');
    expect(req.request.method).toBe('GET');
    req.flush([key]);

    expect(service.available()).toBe(true);
    expect(service.keys()).toEqual([key]);
  });

  it('list() marks the endpoint unavailable on 404 and leaves other errors alone', () => {
    let failed = 0;
    service.list().subscribe({ error: () => failed++ });
    http.expectOne('/api/v1/auth/api-keys').flush('not found', { status: 404, statusText: 'Not Found' });
    expect(service.available()).toBe(false);
    expect(failed).toBe(1);

    service.list().subscribe({ error: () => failed++ });
    http.expectOne('/api/v1/auth/api-keys').flush('down', { status: 503, statusText: 'Unavailable' });
    expect(service.available()).toBe(false);
    expect(failed).toBe(2);
  });

  it('create() posts the body, appends the key and hands back the secret once', () => {
    let created: { secret: string } | undefined;
    service.create({ name: 'QGIS laptop', classification: 'UNCLASSIFIED', expiresInDays: 30 }).subscribe((c) => (created = c));

    const req = http.expectOne('/api/v1/auth/api-keys');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ name: 'QGIS laptop', classification: 'UNCLASSIFIED', expiresInDays: 30 });
    req.flush({ ...key, expiresAt: '2026-10-31T00:00:00Z', secret: 'sk_abc1234567' });

    expect(created?.secret).toBe('sk_abc1234567');
    expect(service.keys()).toEqual([{ ...key, expiresAt: '2026-10-31T00:00:00Z' }]);
    expect('secret' in service.keys()[0]).toBe(false);
  });

  it('revoke() deletes the key and drops it from the signal', () => {
    service.list().subscribe();
    http.expectOne('/api/v1/auth/api-keys').flush([key, { ...key, id: 'k2', name: 'Other' }]);

    service.revoke('k1').subscribe();
    const req = http.expectOne('/api/v1/auth/api-keys/k1');
    expect(req.request.method).toBe('DELETE');
    req.flush(null, { status: 204, statusText: 'No Content' });

    expect(service.keys().map((k) => k.id)).toEqual(['k2']);
  });
});
