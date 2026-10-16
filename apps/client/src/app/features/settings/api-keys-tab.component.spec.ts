import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ApiKeysTabComponent } from './api-keys-tab.component';
import { ApiKey } from '../../core/services/api-key.service';
import { AuthService } from '../../core/services/auth.service';

const existing: ApiKey = {
  id: 'k1',
  name: 'QGIS',
  prefix: 'sk_qgis',
  classification: 'UNCLASSIFIED',
  createdAt: '2026-10-01T00:00:00Z',
  lastUsedAt: '2026-10-02T00:00:00Z',
};

describe('ApiKeysTabComponent', () => {
  let fixture: ComponentFixture<ApiKeysTabComponent>;
  let component: ApiKeysTabComponent;
  let el: HTMLElement;
  let httpMock: HttpTestingController;

  function button(text: string, root: ParentNode = el): HTMLButtonElement {
    return Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text)!;
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ApiKeysTabComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { getClassificationLevel: () => 'classification-s' } },
      ],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(ApiKeysTabComponent);
    component = fixture.componentInstance;
    el = fixture.nativeElement as HTMLElement;
    fixture.detectChanges();
    httpMock.expectOne('/api/v1/auth/api-keys').flush([existing]);
    fixture.detectChanges();
  });

  afterEach(() => httpMock.verify());

  it('lists the keys and caps the classification choices at the clearance', () => {
    const cells = Array.from(el.querySelectorAll('tbody tr td')).map((td) => td.textContent?.trim());
    expect(cells[0]).toBe('QGIS');
    expect(cells[1]).toBe('sk_qgis…');
    expect(cells[2]).toBe('UNCLASSIFIED');
    expect(cells[4]).toBe('never');
    expect(cells[5]).not.toBe('never');

    const options = Array.from(el.querySelectorAll<HTMLOptionElement>('select option')).map((o) => o.value);
    expect(options).toEqual(['UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET']);
  });

  it('creates a key and reveals the secret once, with a copy button', async () => {
    const writeText = jest.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });

    expect(button('Create key').disabled).toBe(true);
    component.form.setValue({ name: 'Ops laptop', classification: 'SECRET', expiresInDays: 30 });
    fixture.detectChanges();
    expect(button('Create key').disabled).toBe(false);

    el.querySelector<HTMLFormElement>('form')!.dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/v1/auth/api-keys');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ name: 'Ops laptop', classification: 'SECRET', expiresInDays: 30 });
    req.flush({
      id: 'k2',
      name: 'Ops laptop',
      prefix: 'sk_ops1',
      classification: 'SECRET',
      createdAt: '2026-10-16T00:00:00Z',
      expiresAt: '2026-11-15T00:00:00Z',
      secret: 'sk_ops1_verysecret',
    });
    fixture.detectChanges();

    const reveal = el.querySelector('.reveal')!;
    expect(reveal.textContent).toContain('not shown again');
    expect(reveal.querySelector<HTMLInputElement>('input')?.value).toBe('sk_ops1_verysecret');
    expect(el.querySelectorAll('tbody tr').length).toBe(2);
    expect(el.querySelector('tbody')?.textContent).not.toContain('sk_ops1_verysecret');
    expect(component.form.controls.name.value).toBe('');

    button('Copy', reveal).click();
    await fixture.whenStable();
    fixture.detectChanges();
    expect(writeText).toHaveBeenCalledWith('sk_ops1_verysecret');
    expect(button('Copied', reveal)).toBeTruthy();

    button('Done', reveal).click();
    fixture.detectChanges();
    expect(el.querySelector('.reveal')).toBeNull();
    expect(component.created()).toBeNull();
  });

  it('revokes a key after an inline confirmation', () => {
    button('Revoke').click();
    fixture.detectChanges();
    expect(el.querySelector('.confirm')?.textContent).toContain('Revoke?');

    button('No').click();
    fixture.detectChanges();
    expect(el.querySelector('.confirm')).toBeNull();

    button('Revoke').click();
    fixture.detectChanges();
    button('Yes').click();
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/v1/auth/api-keys/k1');
    expect(req.request.method).toBe('DELETE');
    req.flush(null, { status: 204, statusText: 'No Content' });
    fixture.detectChanges();

    expect(el.querySelector('tbody tr')).toBeNull();
    expect(el.querySelector('.empty')?.textContent).toContain('No keys yet');
  });

  it('surfaces a failed create', () => {
    component.form.setValue({ name: 'x', classification: 'UNCLASSIFIED', expiresInDays: null });
    component.create();
    const req = httpMock.expectOne('/api/v1/auth/api-keys');
    expect(req.request.body).toEqual({ name: 'x', classification: 'UNCLASSIFIED', expiresInDays: undefined });
    req.flush({ error: 'too many keys' }, { status: 422, statusText: 'Unprocessable Entity' });
    fixture.detectChanges();

    expect(el.querySelector('.toast.error')?.textContent).toContain('too many keys');
    expect(component.created()).toBeNull();
  });
});
