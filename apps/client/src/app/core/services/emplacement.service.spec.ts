import { TestBed } from '@angular/core/testing';
import { HttpErrorResponse, provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { EMPLACEMENT_WRITER_ROLES, EmplacementService, errorMessage } from './emplacement.service';
import { AuthService } from './auth.service';
import { Emplacement, newDraft } from '../../shared/models/emplacement.model';

const e1 = { id: 'e1', name: 'Bravo', version: 3 } as Emplacement;
const e2 = { id: 'e2', name: 'alpha', version: 1 } as Emplacement;

describe('EmplacementService', () => {
  let service: EmplacementService;
  let http: HttpTestingController;
  const auth = { hasAnyRole: jest.fn().mockReturnValue(true) };

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: auth }],
    });
    service = TestBed.inject(EmplacementService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('loads the list sorted by name', () => {
    service.load().subscribe();
    http.expectOne('/api/v1/emplacements').flush([e1, e2]);
    expect(service.emplacements().map((e) => e.id)).toEqual(['e2', 'e1']);
  });

  it('sends the version it edited as If-Match and replaces the row', () => {
    service.emplacements.set([e1]);
    service.update('e1', 3, { boresightDeg: 90 }).subscribe();
    const req = http.expectOne('/api/v1/emplacements/e1');
    expect(req.request.method).toBe('PATCH');
    expect(req.request.headers.get('If-Match')).toBe('"3"');
    req.flush({ ...e1, boresightDeg: 90, version: 4 });
    expect(service.emplacements()[0].version).toBe(4);
  });

  // Review focus: the second of two editors is told why.
  it('words a 412 as a change by someone else', () => {
    expect(errorMessage(new HttpErrorResponse({ status: 412 }))).toMatch(/changed by someone else/i);
    expect(errorMessage(new HttpErrorResponse({ status: 422, error: { details: [{ path: '/position', message: 'is required for a FIXED emplacement' }] } })))
      .toBe('position is required for a FIXED emplacement');
  });

  it('removes a deleted emplacement and clears its selection', () => {
    service.emplacements.set([e1, e2]);
    service.select('e1');
    service.remove('e1').subscribe();
    http.expectOne('/api/v1/emplacements/e1').flush(null, { status: 204, statusText: 'No Content' });
    expect(service.emplacements().map((e) => e.id)).toEqual(['e2']);
    expect(service.selectedId()).toBeNull();
  });

  it('sets the draft position from a map pick and stops picking', () => {
    service.draft.set(newDraft());
    service.picking.set(true);
    service.pickAt(51.5, -0.1);
    expect(service.draft()?.position).toEqual({ lat: 51.5, lon: -0.1 });
    expect(service.picking()).toBe(false);
  });

  it('leaves writing to operators and admins, as the gateway does', () => {
    expect(EMPLACEMENT_WRITER_ROLES).toEqual(['sentinel-operator', 'sentinel-admin']);
  });

  it('holds a place for the map to fly to', () => {
    expect(service.focus()).toBeNull();
    service.focus.set({ lat: 50, lon: -4 });
    expect(service.focus()).toEqual({ lat: 50, lon: -4 });
  });
});
