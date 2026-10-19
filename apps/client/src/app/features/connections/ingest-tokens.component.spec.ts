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
  function create(clearance = 'SECRET') {
    TestBed.configureTestingModule({
      imports: [IngestTokensComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: { getClassificationLevel: () => clearance } }],
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

  function submit(el: HTMLElement, text = 'sensor one') {
    const name = el.querySelector('input[name="name"]') as HTMLInputElement;
    name.value = text;
    name.dispatchEvent(new Event('input'));
    (el.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
  }

  it('shows the detail of a 422', () => {
    const { fixture, http, el } = create();
    submit(el);
    http.expectOne(URL).flush({ details: [{ path: '/classification', message: 'is above your clearance' }] }, { status: 422, statusText: 'Unprocessable Entity' });
    fixture.detectChanges();
    expect(el.querySelector('.error')?.textContent).toContain('classification is above your clearance');
  });

  it('sends one create while the first is in flight', () => {
    const { fixture, http, el } = create();
    submit(el);
    submit(el);
    const post = http.expectOne(URL); // throws if a second POST went out
    fixture.detectChanges();
    expect((el.querySelector('button[type="submit"]') as HTMLButtonElement).disabled).toBe(true);
    post.flush({ ...row, id: 't2', token: 'si_' + 'y'.repeat(43) });
    http.expectOne(URL).flush([row]);
  });

  it('refuses another create while a secret is on screen', () => {
    const { fixture, http, el } = create();
    submit(el);
    http.expectOne(URL).flush({ ...row, id: 't2', token: 'si_' + 'y'.repeat(43) });
    http.expectOne(URL).flush([row]);
    fixture.detectChanges();
    expect((el.querySelector('button[type="submit"]') as HTMLButtonElement).disabled).toBe(true);
    submit(el, 'another');
    http.expectNone(URL);

    (el.querySelector('.secret button[name="dismiss"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect((el.querySelector('button[type="submit"]') as HTMLButtonElement).disabled).toBe(false);
  });

  it('shows a note and no form when the clearance does not reach the connection', () => {
    const { el } = create('UNCLASSIFIED');
    expect(el.querySelector('form')).toBeNull();
    expect(el.textContent).toContain('Your clearance does not reach this connection\'s classification.');
  });

  it('labels the revoke button for the confirm step', () => {
    const { fixture, el } = create();
    const revoke = () => el.querySelector('button[name="revoke"]') as HTMLButtonElement;
    expect(revoke().textContent?.trim()).toBe('Revoke');
    expect(revoke().getAttribute('aria-label')).toBe('Revoke old');
    revoke().click();
    fixture.detectChanges();
    expect(revoke().textContent?.trim()).toBe('Confirm revoke');
    expect(revoke().getAttribute('aria-label')).toBe('Confirm revoke old');
  });

  it('keeps the error and reloads after a failed revoke', () => {
    const { fixture, http, el } = create();
    const revoke = () => el.querySelector('button[name="revoke"]') as HTMLButtonElement;
    revoke().click();
    revoke().click();
    http.expectOne(`${URL}/t1`).flush({ error: 'not found' }, { status: 404, statusText: 'Not Found' });
    http.expectOne(URL).flush([]);
    fixture.detectChanges();
    expect(el.querySelector('.error')?.textContent).toContain('not found');
    expect(el.querySelector('button[name="revoke"]')).toBeNull();
  });

  it('marks an expired token without taking its revoke button away', () => {
    const { fixture, http, el } = create();
    submit(el);
    http.expectOne(URL).flush({ ...row, id: 't2', token: 'si_' + 'y'.repeat(43) });
    http.expectOne(URL).flush([{ ...row, expiresAt: '2020-01-01T00:00:00Z' }]);
    fixture.detectChanges();
    expect(el.querySelector('tbody')?.textContent).toContain('expired');
    expect(el.querySelector('button[name="revoke"]')).not.toBeNull();
  });

  it('says so when the secret cannot be copied', async () => {
    const original = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: jest.fn().mockRejectedValue(new Error('denied')) }, configurable: true });
    try {
      const { fixture, http, el } = create();
      submit(el);
      http.expectOne(URL).flush({ ...row, id: 't2', token: 'si_' + 'y'.repeat(43) });
      http.expectOne(URL).flush([row]);
      fixture.detectChanges();
      (el.querySelector('.secret button[name="copy"]') as HTMLButtonElement).click();
      await fixture.whenStable();
      fixture.detectChanges();
      expect(el.querySelector('.copy-state')?.textContent).toContain('Copy failed');
    } finally {
      if (original) Object.defineProperty(navigator, 'clipboard', original);
      else delete (navigator as unknown as Record<string, unknown>)['clipboard'];
    }
  });
});
