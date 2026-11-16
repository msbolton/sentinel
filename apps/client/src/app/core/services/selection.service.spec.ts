import { signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { Classification, Entity, EntitySource, EntityType } from '../../shared/models/entity.model';
import { LatLon } from '../../shared/geo-math';
import { EntityService } from './entity.service';
import { MapViewportService } from './map-viewport.service';
import { ReplayMode, ReplayService } from './replay.service';
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
  let viewport: MapViewportService;
  let mode: ReturnType<typeof signal<ReplayMode>>;

  beforeEach(() => {
    entityService = { getEntityById: jest.fn() };
    mode = signal<ReplayMode>('live');
    TestBed.configureTestingModule({
      providers: [
        { provide: EntityService, useValue: entityService },
        { provide: ReplayService, useValue: { mode } },
      ],
    });
    service = TestBed.inject(SelectionService);
    viewport = TestBed.inject(MapViewportService);
  });

  it('flies to a selected track before the map has reported a view', () => {
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    service.select({ ...base, position: { latitude: 12, longitude: 44 } });
    expect(service.entity()?.id).toBe(base.id);
    expect(flights).toEqual([{ lat: 12, lon: 44 }]);
  });

  it('does not fly to a track already inside the view', () => {
    viewport.set({ north: 20, south: 0, east: 50, west: 40 });
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    service.select({ ...base, position: { latitude: 12, longitude: 44 } });
    expect(service.entity()?.id).toBe(base.id);
    expect(flights).toEqual([]);
  });

  it('flies to a track outside the view', () => {
    viewport.set({ north: 20, south: 0, east: 50, west: 40 });
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    service.select({ ...base, position: { latitude: 30, longitude: 44 } });
    expect(flights).toEqual([{ lat: 30, lon: 44 }]);
  });

  it('flyTo moves the camera without touching the selection', () => {
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    service.flyTo({ lat: 1, lon: 2 });
    expect(flights).toEqual([{ lat: 1, lon: 2 }]);
    expect(service.entity()).toBeNull();
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
    expect(service.selectById('nope')).toBe(false);
    expect(entityService.getEntityById).toHaveBeenCalledWith('nope');
    expect(service.entity()).toBeNull();
    expect(flights).toEqual([]);
  });

  it('selectById selects and flies to a known entity', () => {
    const known = { ...base, position: { latitude: -3, longitude: 120, altitude: 9000 } };
    entityService.getEntityById.mockReturnValue(known);
    const flights: LatLon[] = [];
    service.fly$.subscribe((p) => flights.push(p));
    expect(service.selectById(known.id)).toBe(true);
    expect(service.entity()).toBe(known);
    expect(flights).toEqual([{ lat: -3, lon: 120 }]);
  });

  describe('while the map replays', () => {
    const known = { ...base, position: { latitude: -3, longitude: 120 } };

    beforeEach(() => mode.set('replay'));

    it('selectById (the alert ticker and track stack path) returns false and selects nothing', () => {
      entityService.getEntityById.mockReturnValue(known);
      const flights: LatLon[] = [];
      service.fly$.subscribe((p) => flights.push(p));

      expect(service.selectById(known.id)).toBe(false);
      expect(service.entity()).toBeNull();
      expect(flights).toEqual([]);
    });

    it('select ignores a live track and keeps the replay pick', () => {
      const replayPick = { ...base, id: 'replayed' };
      service.entity.set(replayPick); // how the map selects a replayed track

      expect(service.select(known)).toBe(false);
      expect(service.entity()).toBe(replayPick);
    });

    it('select(null) still clears', () => {
      service.entity.set(known);
      expect(service.select(null)).toBe(true);
      expect(service.entity()).toBeNull();
    });
  });
});
