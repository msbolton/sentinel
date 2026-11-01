import { TestBed } from '@angular/core/testing';
import { Classification, Entity, EntitySource, EntityType } from '../../shared/models/entity.model';
import { LatLon } from '../../shared/geo-math';
import { EntityService } from './entity.service';
import { SelectionService } from './selection.service';

const base: Entity = {
  id: 'trk-1',
  entityType: EntityType.AIRCRAFT,
  name: 'TRK 1',
  source: EntitySource.OPENSKY,
  classification: Classification.UNCLASSIFIED,
  metadata: {},
  affiliations: [],
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-01T00:00:00Z',
};

describe('SelectionService', () => {
  let service: SelectionService;
  let entityService: { getEntityById: jest.Mock };

  beforeEach(() => {
    entityService = { getEntityById: jest.fn() };
    TestBed.configureTestingModule({ providers: [{ provide: EntityService, useValue: entityService }] });
    service = TestBed.inject(SelectionService);
  });

  it('selecting an entity with a position asks the map to fly there', () => {
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    service.select({ ...base, position: { latitude: 12, longitude: 44 } });
    expect(service.entity()?.id).toBe(base.id);
    expect(flights).toEqual([{ lat: 12, lon: 44 }]);
  });

  it('selecting an entity without a position does not fly', () => {
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    service.select(base);
    expect(service.entity()).toBe(base);
    expect(flights).toEqual([]);
  });

  it('selectById ignores unknown ids', () => {
    entityService.getEntityById.mockReturnValue(undefined);
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    service.selectById('nope');
    expect(entityService.getEntityById).toHaveBeenCalledWith('nope');
    expect(service.entity()).toBeNull();
    expect(flights).toEqual([]);
  });

  it('selectById selects and flies to a known entity', () => {
    const known = { ...base, position: { latitude: -3, longitude: 120, altitude: 9000 } };
    entityService.getEntityById.mockReturnValue(known);
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    service.selectById(known.id);
    expect(service.entity()).toBe(known);
    expect(flights).toEqual([{ lat: -3, lon: 120 }]);
  });
});
