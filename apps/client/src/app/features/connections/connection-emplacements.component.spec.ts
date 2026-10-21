import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Router } from '@angular/router';
import { ConnectionEmplacementsComponent } from './connection-emplacements.component';
import { EmplacementService } from '../../core/services/emplacement.service';
import { AuthService } from '../../core/services/auth.service';
import { Feed } from '../../shared/models/feed.model';
import { Emplacement } from '../../shared/models/emplacement.model';

const today = (hhmm: string) => {
  const d = new Date();
  const [h, m] = hhmm.split(':').map(Number);
  d.setUTCHours(h, m, 0, 0);
  return d.toISOString();
};

function row(id: string, over: Partial<Emplacement> = {}): Emplacement {
  return {
    id, name: id, description: '', mobility: 'FIXED', sensorType: 'RF', position: { lat: 50, lon: -4 }, altitude: null,
    boresightDeg: 0, azimuthReference: 'TRUE_NORTH', coverage: { minRangeM: 0, maxRangeM: 40000, azimuthWidthDeg: 120 },
    defaultSigma: {}, feedId: 'grpc-1', classification: 'UNCLASSIFIED', exercise: false, active: true, lastReportAt: null,
    createdBy: 'u', createdAt: '', updatedAt: '', version: 1, ...over,
  };
}

describe('ConnectionEmplacementsComponent', () => {
  const auth = { hasAnyRole: jest.fn().mockReturnValue(true), getClassificationLevel: jest.fn().mockReturnValue('SECRET') };

  function create(feed: Feed, rows: Emplacement[]) {
    TestBed.configureTestingModule({
      imports: [ConnectionEmplacementsComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: auth }, { provide: Router, useValue: { navigateByUrl: jest.fn() } }],
    });
    const service = TestBed.inject(EmplacementService);
    service.emplacements.set(rows);
    const fixture = TestBed.createComponent(ConnectionEmplacementsComponent);
    fixture.componentRef.setInput('feed', feed);
    fixture.detectChanges();
    return { fixture, service, http: TestBed.inject(HttpTestingController), el: fixture.nativeElement as HTMLElement };
  }

  const grpc = {
    id: 'grpc-1', name: 'EW sim', driver: 'grpc', config: { exercise: true, classification: 'UNCLASSIFIED' },
    status: { detail: { emplacements: [
      { id: 'a', name: 'a', classification: 'UNCLASSIFIED', state: 'online', intervalSec: 5 },
      { id: 'b', name: 'b', classification: 'UNCLASSIFIED', state: 'offline', lastHeartbeatAt: today('14:02'), intervalSec: 5 },
      { id: 'c', name: 'c', classification: 'UNCLASSIFIED', state: 'pending', intervalSec: 10 },
    ] } },
  } as unknown as Feed;

  it("lists the connection's emplacements with their heartbeat badges and intervals", () => {
    const { el } = create(grpc, [row('a'), row('b'), row('c'), row('elsewhere', { feedId: 'grpc-2' })]);
    const rows = Array.from(el.querySelectorAll('.emp-row'));
    expect(rows.map((r) => r.querySelector('.emp-name')?.textContent?.trim())).toEqual(['a', 'b', 'c']);
    expect(rows.map((r) => r.querySelector('.badge')?.textContent?.trim())).toEqual(['online', 'silent since 14:02Z', 'awaiting first heartbeat']);
    expect(rows[0].textContent).toContain('every 5 s');
  });

  it('starts a new emplacement locked to the connection, for a grpc connection only', () => {
    const { el, fixture, service } = create(grpc, []);
    (el.querySelector('button.new-emplacement') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(service.draft()).toEqual(expect.objectContaining({ id: null, feedId: 'grpc-1', exercise: true }));
    expect(el.querySelector('app-emplacement-editor')).not.toBeNull();

    TestBed.resetTestingModule();
    const tcp = { id: 'tcp-1', name: 'TCP', driver: 'tcp', config: {}, status: { detail: {} } } as unknown as Feed;
    const second = create(tcp, [row('legacy', { feedId: 'tcp-1' })]);
    expect(second.el.querySelector('button.new-emplacement')).toBeNull();
    expect(second.el.querySelector('.emp-row .badge')).toBeNull(); // a tcp site has no heartbeat to show
  });

  it('deactivates an emplacement with a PATCH that keeps its connection', () => {
    const { el, http } = create(grpc, [row('a')]);
    (el.querySelector('button.deactivate') as HTMLButtonElement).click();
    const req = http.expectOne('/api/v1/emplacements/a');
    expect(req.request.method).toBe('PATCH');
    expect(req.request.body).toEqual({ active: false });
    expect(req.request.headers.get('If-Match')).toBe('"1"');
    req.flush(row('a', { active: false, version: 2 }));
    http.verify();
  });

  describe('moving straight from one editor to another', () => {
    const editOf = (el: HTMLElement, name: string) =>
      Array.from(el.querySelectorAll('.emp-row'))
        .find((r) => r.querySelector('.emp-name')?.textContent?.trim() === name)!
        .querySelector('.emp-actions button') as HTMLButtonElement;
    const nameInput = (el: HTMLElement) => el.querySelector('app-emplacement-editor input[name="name"]') as HTMLInputElement | null;

    it("opens B's editor when Edit on B is clicked while A's is open", () => {
      const { el, fixture, service } = create(grpc, [row('a'), row('b')]);
      editOf(el, 'a').click();
      fixture.detectChanges();
      expect(nameInput(el)?.value).toBe('a');
      editOf(el, 'b').click();
      fixture.detectChanges();
      expect(service.draft()).toEqual(expect.objectContaining({ id: 'b', name: 'b' }));
      expect(el.querySelectorAll('app-emplacement-editor').length).toBe(1);
      expect(nameInput(el)?.value).toBe('b');
    });

    it('opens an empty editor when + New is clicked while a row is being edited', () => {
      const { el, fixture, service } = create(grpc, [row('a')]);
      editOf(el, 'a').click();
      fixture.detectChanges();
      (el.querySelector('button.new-emplacement') as HTMLButtonElement).click();
      fixture.detectChanges();
      expect(service.draft()).toEqual(expect.objectContaining({ id: null, feedId: 'grpc-1' }));
      expect(el.querySelectorAll('app-emplacement-editor').length).toBe(1);
      expect(nameInput(el)?.value).toBe('');
    });
  });
});
