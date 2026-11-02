import { TestBed } from '@angular/core/testing';
import { MapCursorService } from './map-cursor.service';

describe('MapCursorService', () => {
  let service: MapCursorService;

  beforeEach(() => {
    TestBed.configureTestingModule({});
    service = TestBed.inject(MapCursorService);
  });

  it('starts with no cursor and no altitude', () => {
    expect(service.cursor()).toBeNull();
    expect(service.cameraAltitudeM()).toBeNull();
  });

  it('holds the last globe pick under the mouse', () => {
    service.setCursor({ lat: 1, lon: 2 });
    expect(service.cursor()).toEqual({ lat: 1, lon: 2 });
  });

  it('clears the cursor when the mouse leaves the globe', () => {
    service.setCursor({ lat: 1, lon: 2 });
    service.setCursor(null);
    expect(service.cursor()).toBeNull();
  });

  it('rejects non-finite coordinates as off-globe', () => {
    service.setCursor({ lat: 1, lon: 2 });
    service.setCursor({ lat: NaN, lon: 2 });
    expect(service.cursor()).toBeNull();
    service.setCursor({ lat: 1, lon: 2 });
    service.setCursor({ lat: 1, lon: Infinity });
    expect(service.cursor()).toBeNull();
  });

  it('holds the camera altitude and drops non-finite values', () => {
    service.setCameraAltitude(12_500);
    expect(service.cameraAltitudeM()).toBe(12_500);
    service.setCameraAltitude(NaN);
    expect(service.cameraAltitudeM()).toBeNull();
    service.setCameraAltitude(null);
    expect(service.cameraAltitudeM()).toBeNull();
  });
});
