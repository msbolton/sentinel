import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { EntitySource, EntityType } from '../../shared/models/entity.model';
import { placeLabel, routeQueryOf } from '../../shared/models/route.model';
import { RouteService } from './route.service';

const aircraft = (source: EntitySource, icaoHex: string, aircraftId?: string) =>
  ({ id: icaoHex, entityType: EntityType.AIRCRAFT, source, platformData: { adsb: { icaoHex, aircraftId } } }) as any;

describe('route model', () => {
  it('asks by callsign, and for IntelSky aircraft even without one', () => {
    expect(routeQueryOf(aircraft(EntitySource.OPENSKY, '400abc', ' baw123 '))).toEqual({ hex: '400ABC', callsign: 'BAW123', intelsky: false });
    expect(routeQueryOf(aircraft(EntitySource.INTELSKY, 'AE7457'))).toEqual({ hex: 'AE7457', callsign: null, intelsky: true });
    expect(routeQueryOf(aircraft(EntitySource.OPENSKY, '400ABC'))).toBeNull(); // nothing to look up
    expect(routeQueryOf(aircraft(EntitySource.OPENSKY, '400ABC', 'BA-1'))).toBeNull();
    expect(routeQueryOf(null)).toBeNull();
  });

  it('labels airports and first-seen points', () => {
    expect(placeLabel({ icao: 'EGLL', iata: 'LHR', name: 'London Heathrow Airport', municipality: 'London', lat: 0, lon: 0 })).toBe('EGLL London Heathrow Airport');
    expect(placeLabel({ icao: 'OTHH', name: 'Hamad International Airport', municipality: 'Doha', lat: 0, lon: 0 })).toBe('OTHH Hamad International Airport · Doha');
    expect(placeLabel({ name: 'First seen today', lat: 0, lon: 0, time: '2026-10-18T07:45:04Z' })).toBe('First seen today 07:45 UTC');
    expect(placeLabel(undefined)).toBe('');
  });
});

describe('RouteService', () => {
  let service: RouteService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    service = TestBed.inject(RouteService);
    http = TestBed.inject(HttpTestingController);
  });
  afterEach(() => http.verify());

  it('loads the route of the selected aircraft once and clears on deselect', () => {
    const a = aircraft(EntitySource.OPENSKY, '400ABC', 'BAW123');
    service.select(a);
    const req = http.expectOne((r) => r.url === '/api/v1/aircraft/400ABC/route' && r.params.get('callsign') === 'BAW123' && !r.params.has('source'));
    req.flush({ hex: '400ABC', provider: 'adsbdb', departure: { name: 'London Heathrow Airport', lat: 51.47, lon: -0.46 } });
    expect(service.state().route?.departure?.name).toBe('London Heathrow Airport');
    service.select(a); // same aircraft: no new request
    service.select(null);
    expect(service.state()).toEqual({ key: null, loading: false, route: null, error: null });
  });

  it('asks IntelSky aircraft with source=INTELSKY', () => {
    service.select(aircraft(EntitySource.INTELSKY, 'AE7457', 'RCH678'));
    http.expectOne((r) => r.params.get('source') === 'INTELSKY' && r.params.get('callsign') === 'RCH678').flush('x', { status: 502, statusText: 'Bad Gateway' });
    expect(service.state().error).toBe('Route unavailable.');
  });
});
