import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { IngestTokenService } from './ingest-token.service';

describe('IngestTokenService', () => {
  let service: IngestTokenService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    service = TestBed.inject(IngestTokenService);
    http = TestBed.inject(HttpTestingController);
  });
  afterEach(() => http.verify());

  it('lists, creates and revokes under the connection, escaping its id', () => {
    service.list('ew 1').subscribe();
    http.expectOne('/api/v1/feeds/ew%201/tokens').flush([]);
    service.create('ew 1', { name: 'sensor' }).subscribe();
    const post = http.expectOne('/api/v1/feeds/ew%201/tokens');
    expect(post.request.method).toBe('POST');
    expect(post.request.body).toEqual({ name: 'sensor' });
    post.flush({});
    service.revoke('ew 1', 't1').subscribe();
    const del = http.expectOne('/api/v1/feeds/ew%201/tokens/t1');
    expect(del.request.method).toBe('DELETE');
    del.flush(null, { status: 204, statusText: 'No Content' });
  });
});
