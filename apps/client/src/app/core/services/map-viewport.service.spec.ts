import { TestBed } from '@angular/core/testing';
import { MapViewportService } from './map-viewport.service';

describe('MapViewportService', () => {
  let service: MapViewportService;

  beforeEach(() => {
    TestBed.configureTestingModule({});
    service = TestBed.inject(MapViewportService);
  });

  it('starts empty until the map reports a view', () => {
    expect(service.bounds()).toBeNull();
  });

  it('keeps a copy of the last reported bounds', () => {
    const bounds = { north: 50, south: 40, east: 10, west: 0 };
    service.set(bounds);
    expect(service.bounds()).toEqual(bounds);
    expect(service.bounds()).not.toBe(bounds);

    service.set({ north: 60, south: 55, east: 20, west: 15 });
    expect(service.bounds()).toEqual({ north: 60, south: 55, east: 20, west: 15 });
  });
});
