import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { signal } from '@angular/core';
import { UnassignedEmplacementsComponent } from './unassigned-emplacements.component';
import { EmplacementService } from '../../core/services/emplacement.service';
import { AuthService } from '../../core/services/auth.service';
import { FeedService } from '../../core/services/feed.service';
import { Emplacement } from '../../shared/models/emplacement.model';

function row(id: string, over: Partial<Emplacement> = {}): Emplacement {
  return {
    id, name: id, description: '', mobility: 'FIXED', sensorType: 'RADAR', position: { lat: 49.96, lon: -5.2 }, altitude: null,
    boresightDeg: 200, azimuthReference: 'TRUE_NORTH', coverage: { minRangeM: 0, maxRangeM: 60000, azimuthWidthDeg: 120 },
    defaultSigma: {}, feedId: 'grpc-1', classification: 'UNCLASSIFIED', exercise: false, active: true, lastReportAt: null,
    createdBy: 'seed', createdAt: '', updatedAt: '', version: 1, ...over,
  };
}

describe('UnassignedEmplacementsComponent', () => {
  const auth = { hasAnyRole: jest.fn().mockReturnValue(true), getClassificationLevel: () => 'SECRET' };
  const feeds = {
    feeds: signal([
      { id: 'grpc-1', name: 'EW sim', driver: 'grpc' },
      { id: 'tcp-1', name: 'TCP', driver: 'tcp' },
    ]),
  };

  function create(rows: Emplacement[]) {
    TestBed.configureTestingModule({
      imports: [UnassignedEmplacementsComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: auth }, { provide: FeedService, useValue: feeds }],
    });
    TestBed.inject(EmplacementService).emplacements.set(rows);
    const fixture = TestBed.createComponent(UnassignedEmplacementsComponent);
    fixture.detectChanges();
    return { fixture, http: TestBed.inject(HttpTestingController), el: fixture.nativeElement as HTMLElement };
  }

  it('lists the emplacements with no connection, offers gRPC connections only, and assigns one', () => {
    const { el, fixture, http } = create([row('lizard', { name: 'Lizard Point radar', feedId: null, active: false }), row('bound')]);
    expect(Array.from(el.querySelectorAll('.unassigned-row .emp-name')).map((n) => n.textContent?.trim())).toEqual(['Lizard Point radar']);
    const select = el.querySelector('select[name="assign-to"]') as HTMLSelectElement;
    expect(Array.from(select.options).map((o) => o.value)).toEqual(['', 'grpc-1']);
    select.value = 'grpc-1';
    select.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    (el.querySelector('button.assign') as HTMLButtonElement).click();
    const req = http.expectOne('/api/v1/emplacements/lizard');
    expect(req.request.method).toBe('PATCH');
    expect(req.request.body).toEqual({ feedId: 'grpc-1', active: true });
    expect(req.request.headers.get('If-Match')).toBe('"1"');
    req.flush(row('lizard', { name: 'Lizard Point radar', version: 2 }));
    fixture.detectChanges();
    expect(el.querySelector('section.unassigned')).toBeNull();
    http.verify();
  });

  it('deletes one', () => {
    const { el, http } = create([row('stray', { feedId: null, active: false })]);
    (el.querySelector('button.btn-delete') as HTMLButtonElement).click();
    const req = http.expectOne('/api/v1/emplacements/stray');
    expect(req.request.method).toBe('DELETE');
    req.flush(null, { status: 204, statusText: 'No Content' });
    http.verify();
  });

  it('is not shown when every emplacement has a connection', () => {
    const { el } = create([row('bound')]);
    expect(el.querySelector('section.unassigned')).toBeNull();
  });
});
