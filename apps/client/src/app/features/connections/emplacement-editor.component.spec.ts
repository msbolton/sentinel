import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Router } from '@angular/router';
import { EmplacementEditorComponent } from './emplacement-editor.component';
import { EmplacementService } from '../../core/services/emplacement.service';
import { AuthService } from '../../core/services/auth.service';
import { Feed } from '../../shared/models/feed.model';
import { newDraft } from '../../shared/models/emplacement.model';

describe('EmplacementEditorComponent', () => {
  const auth = { hasAnyRole: jest.fn().mockReturnValue(true), getClassificationLevel: jest.fn().mockReturnValue('SECRET') };
  const router = { navigateByUrl: jest.fn().mockResolvedValue(true) };
  const feed = { id: 'grpc-1', name: 'EW sim', driver: 'grpc', config: { exercise: true, classification: 'CONFIDENTIAL' } } as unknown as Feed;

  function create() {
    TestBed.configureTestingModule({
      imports: [EmplacementEditorComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: auth }, { provide: Router, useValue: router }],
    });
    const service = TestBed.inject(EmplacementService);
    service.draft.set({ ...newDraft(), name: 'Mast', position: { lat: 50, lon: -4 }, feedId: 'grpc-1' });
    const fixture = TestBed.createComponent(EmplacementEditorComponent);
    fixture.componentRef.setInput('feed', feed);
    fixture.detectChanges();
    return { fixture, service, http: TestBed.inject(HttpTestingController), el: fixture.nativeElement as HTMLElement };
  }

  it('is locked to its connection: no connection picker, the exercise box on, levels from the connection up to the clearance', () => {
    const { el } = create();
    expect(el.querySelector('select[name="feedId"]')).toBeNull();
    expect(el.textContent).toContain('EW sim');
    const exercise = el.querySelector('input[name="exercise"]') as HTMLInputElement;
    expect(exercise.checked).toBe(true);
    expect(exercise.disabled).toBe(true);
    const levels = Array.from(el.querySelectorAll('select[name="classification"] option')).map((o) => o.textContent?.trim());
    expect(levels).toEqual(['CONFIDENTIAL', 'SECRET']);
    expect(el.textContent).not.toContain('Pick on map');
  });

  it('saves to its connection, as exercise, at no less than its level', () => {
    const { el, http } = create();
    (el.querySelector('button[type="submit"]') as HTMLButtonElement).click();
    const req = http.expectOne('/api/v1/emplacements');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual(expect.objectContaining({ feedId: 'grpc-1', exercise: true, classification: 'CONFIDENTIAL', name: 'Mast', position: { lat: 50, lon: -4 } }));
    req.flush({ ...req.request.body, id: 'e1', version: 1 });
    http.verify();
  });

  it('has a "Height above ground (m)" field after the altitude, and sends heightAboveGroundM', () => {
    const { el, http } = create();
    const labels = Array.from(el.querySelectorAll('span.field-label')).map((l) => l.textContent?.trim());
    expect(labels).toContain('Height above ground (m)');
    const altitude = el.querySelector('input[name="altitude"]') as HTMLInputElement;
    const height = el.querySelector('input[name="heightAboveGroundM"]') as HTMLInputElement;
    expect(altitude.compareDocumentPosition(height) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(height.value).toBe('');
    height.value = '12.5';
    height.dispatchEvent(new Event('input'));
    (el.querySelector('button[type="submit"]') as HTMLButtonElement).click();
    const req = http.expectOne('/api/v1/emplacements');
    expect(req.request.body).toEqual(expect.objectContaining({ heightAboveGroundM: 12.5 }));
    req.flush({ ...req.request.body, id: 'e1', version: 1 });
    http.verify();
  });

  it('sends no heightAboveGroundM when the field is empty, and shows the stored one', () => {
    const { el, http, service, fixture } = create();
    service.draft.set({ ...service.draft()!, heightAboveGroundM: 8 });
    fixture.detectChanges();
    expect((el.querySelector('input[name="heightAboveGroundM"]') as HTMLInputElement).value).toBe('8');
    const height = el.querySelector('input[name="heightAboveGroundM"]') as HTMLInputElement;
    height.value = '';
    height.dispatchEvent(new Event('input'));
    (el.querySelector('button[type="submit"]') as HTMLButtonElement).click();
    const req = http.expectOne('/api/v1/emplacements');
    expect(req.request.body).not.toHaveProperty('heightAboveGroundM');
    req.flush({ ...req.request.body, id: 'e1', version: 1 });
    http.verify();
  });

  it('shows the position on the map, and clears the draft when it goes', () => {
    const { el, fixture, service } = create();
    (el.querySelector('button.show-on-map') as HTMLButtonElement).click();
    expect(service.focus()).toEqual({ lat: 50, lon: -4 });
    expect(router.navigateByUrl).toHaveBeenCalledWith('/map');
    fixture.destroy();
    expect(service.draft()).toBeNull();
  });
});
