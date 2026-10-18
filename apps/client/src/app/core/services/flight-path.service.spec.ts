import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { EntitySource, EntityType } from '../../shared/models/entity.model';
import { FlightPathService } from './flight-path.service';

const aircraft = (id: string, source: EntitySource, icaoHex: string) =>
  ({ id, entityType: EntityType.AIRCRAFT, source, platformData: { adsb: { icaoHex } } }) as any;

describe('FlightPathService', () => {
  let service: FlightPathService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    service = TestBed.inject(FlightPathService);
    http = TestBed.inject(HttpTestingController);
  });
  afterEach(() => http.verify());

  it("loads today's path for a selected IntelSky aircraft", () => {
    service.select(aircraft('a', EntitySource.INTELSKY, 'ae7457'));
    expect(service.state()).toEqual(expect.objectContaining({ hex: 'AE7457', days: 1, loading: true }));
    const req = http.expectOne((r) => r.url === '/api/v1/aircraft/AE7457/path' && r.params.get('days') === '1');
    req.flush({ hex: 'AE7457', source: 'INTELSKY', days: 1, points: [{ lat: 1, lon: 2, altFt: 3, ts: 4 }] });
    expect(service.state().path?.points.length).toBe(1);
    expect(service.state().loading).toBe(false);
  });

  it('asks for nothing for other aircraft until a range is chosen', () => {
    service.select(aircraft('b', EntitySource.OPENSKY, 'A1B2C3'));
    http.expectNone(() => true);
    expect(service.state()).toEqual(expect.objectContaining({ hex: 'A1B2C3', days: 0 }));
    service.setDays(3);
    http.expectOne((r) => r.url === '/api/v1/aircraft/A1B2C3/path' && r.params.get('days') === '3').flush({ hex: 'A1B2C3', source: 'INTELSKY', days: 3, points: [] });
    expect(service.state().path?.points).toEqual([]);
  });

  it('drops an answer for an aircraft no longer selected and clears on deselect', () => {
    service.select(aircraft('a', EntitySource.INTELSKY, 'AE7457'));
    const first = http.expectOne('/api/v1/aircraft/AE7457/path?days=1');
    service.select(null);
    expect(first.cancelled).toBe(true);
    expect(service.state()).toEqual({ hex: null, days: 0, loading: false, path: null, error: null });
  });

  it('says when IntelSky is busy', () => {
    service.select(aircraft('a', EntitySource.INTELSKY, 'AE7457'));
    http.expectOne('/api/v1/aircraft/AE7457/path?days=1').flush('busy', { status: 503, statusText: 'Service Unavailable' });
    expect(service.state().error).toBe('IntelSky is busy; try again shortly.');
  });
});
