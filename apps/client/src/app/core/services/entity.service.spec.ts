import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { EntityService, normalizeEntity } from './entity.service';
import { WebSocketService } from './websocket.service';
import { Entity } from '../../shared/models/entity.model';

describe('normalizeEntity', () => {
  it('maps the REST {lat, lon} shape and top-level altitude to the client shape', () => {
    const raw = { id: 'a', position: { lat: 41.9, lon: -87.9 }, altitude: 10972 } as unknown as Entity;
    expect(normalizeEntity(raw).position).toEqual({ latitude: 41.9, longitude: -87.9, altitude: 10972 });
  });

  it('omits altitude when the REST payload has none', () => {
    const raw = { id: 'a', position: { lat: 1, lon: 2 }, altitude: null } as unknown as Entity;
    expect(normalizeEntity(raw).position).toEqual({ latitude: 1, longitude: 2 });
  });

  it('leaves the client shape untouched', () => {
    const entity = { id: 'a', position: { latitude: 1, longitude: 2, altitude: 3 } } as Entity;
    expect(normalizeEntity(entity)).toBe(entity);
  });

  it('fills a missing altitude on the client shape from the top-level field', () => {
    const raw = { id: 'a', position: { latitude: 1, longitude: 2 }, altitude: 300 } as unknown as Entity;
    expect(normalizeEntity(raw).position).toEqual({ latitude: 1, longitude: 2, altitude: 300 });
  });

  it('passes entities without a position through', () => {
    const entity = { id: 'a' } as Entity;
    expect(normalizeEntity(entity)).toBe(entity);
  });
});

describe('EntityService', () => {
  let service: EntityService;
  let http: HttpTestingController;
  const entityUpdates$ = new Subject();
  const ageoutEvents$ = new Subject();

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: WebSocketService, useValue: { entityUpdates$, ageoutEvents$ } },
      ],
    });
    service = TestBed.inject(EntityService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
    service.ngOnDestroy();
  });

  it('sends the bbox and the `types` parameter the entity-service reads', () => {
    service.getEntities({ entityTypes: ['AIRCRAFT' as any], north: 45, south: 40, east: -80, west: -90, pageSize: 500 }).subscribe();

    const req = http.expectOne((r) => r.url === '/api/v1/entities');
    expect(req.request.params.get('types')).toBe('AIRCRAFT');
    expect(req.request.params.has('entityTypes')).toBe(false);
    expect(req.request.params.get('north')).toBe('45');
    expect(req.request.params.get('west')).toBe('-90');
    expect(req.request.params.get('pageSize')).toBe('500');
    req.flush({ data: [], total: 0 });
  });

  it('normalises REST positions into the store and the response', (done) => {
    service.getEntities({ pageSize: 10 }).subscribe((response) => {
      expect(response.data[0].position).toEqual({ latitude: 41.9, longitude: -87.9, altitude: 10972 });
      expect(service.getEntityById('a')?.position?.latitude).toBe(41.9);
      done();
    });
    http.expectOne((r) => r.url === '/api/v1/entities').flush({
      data: [{ id: 'a', entityType: 'AIRCRAFT', position: { lat: 41.9, lon: -87.9 }, altitude: 10972 }],
      total: 1,
    });
  });

  it('normalises a single entity fetch', (done) => {
    service.getEntity('a').subscribe((entity) => {
      expect(entity.position).toEqual({ latitude: 1, longitude: 2 });
      done();
    });
    http.expectOne('/api/v1/entities/a').flush({ id: 'a', position: { lat: 1, lon: 2 } });
  });

  it('asks the entity-service for counts per type and cell in a box', () => {
    let total = -1;
    service.getDensity({ north: 70, south: 20, east: 40, west: -30 }, 10).subscribe((d) => (total = d.total));
    const req = http.expectOne((r) => r.url === '/api/v1/entities/density');
    expect(req.request.params.get('north')).toBe('70');
    expect(req.request.params.get('west')).toBe('-30');
    expect(req.request.params.get('cell')).toBe('10');
    req.flush({ cellDegrees: 10, generatedAt: '2026-10-18T12:00:00Z', total: 3, cells: [['VESSEL', 50.5, -1.5, 3]] });
    expect(total).toBe(3);
  });
});
