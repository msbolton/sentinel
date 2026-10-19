import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { IngestTokensComponent } from './ingest-tokens.component';
import { AuthService } from '../../core/services/auth.service';
import { Feed } from '../../shared/models/feed.model';

const URL = '/api/v1/feeds/ew-1/tokens';
const feed = { id: 'ew-1', name: 'EW', driver: 'grpc', config: { classification: 'CONFIDENTIAL' } } as unknown as Feed;
const row = { id: 't1', feedId: 'ew-1', name: 'old', prefix: 'si_abcdefgh', classification: 'CONFIDENTIAL', createdBy: 'u', createdAt: '2026-10-19T00:00:00Z', lastUsedAt: null, expiresAt: null, revokedAt: null };

describe('IngestTokensComponent', () => {
  function create() {
    TestBed.configureTestingModule({
      imports: [IngestTokensComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: { getClassificationLevel: () => 'SECRET' } }],
    });
    const fixture = TestBed.createComponent(IngestTokensComponent);
    fixture.componentRef.setInput('feed', feed);
    const http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http.expectOne(URL).flush([row]);
    fixture.detectChanges();
    return { fixture, http, el: fixture.nativeElement as HTMLElement };
  }

  it('offers levels from the connection\'s up to the user\'s clearance', () => {
    const { el } = create();
    const levels = Array.from(el.querySelectorAll('select[name="classification"] option')).map((o) => o.textContent?.trim());
    expect(levels).toEqual(['CONFIDENTIAL', 'SECRET']);
  });

  // Review focus 5.
  it('shows a new secret once, then forgets it', () => {
    const { fixture, http, el } = create();
    const name = el.querySelector('input[name="name"]') as HTMLInputElement;
    name.value = 'sensor one';
    name.dispatchEvent(new Event('input'));
    (el.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
    const post = http.expectOne(URL);
    expect(post.request.body).toEqual({ name: 'sensor one', classification: 'CONFIDENTIAL' });
    post.flush({ ...row, id: 't2', name: 'sensor one', token: 'si_' + 'x'.repeat(43) });
    http.expectOne(URL).flush([row, { ...row, id: 't2', name: 'sensor one' }]);
    fixture.detectChanges();
    expect(el.querySelector('.secret code')?.textContent).toBe('si_' + 'x'.repeat(43));

    (el.querySelector('.secret button[name="dismiss"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el.querySelector('.secret')).toBeNull();
    expect(el.textContent).not.toContain('x'.repeat(43));
  });

  it('revokes after a second click and reloads', () => {
    const { fixture, http, el } = create();
    const revoke = () => el.querySelector('button[name="revoke"]') as HTMLButtonElement;
    revoke().click();
    fixture.detectChanges();
    http.expectNone(`${URL}/t1`);
    revoke().click(); // the confirm step
    http.expectOne(`${URL}/t1`).flush(null, { status: 204, statusText: 'No Content' });
    http.expectOne(URL).flush([{ ...row, revokedAt: '2026-10-19T01:00:00Z' }]);
    fixture.detectChanges();
    expect(el.textContent).toContain('revoked');
  });
});
