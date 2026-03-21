import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TrackApiService, TrackPoint } from './track-api.service';
import { AuthService } from './auth.service';

describe('TrackApiService', () => {
  let service: TrackApiService;
  let httpMock: HttpTestingController;

  beforeEach(() => {
    const authMock = { getToken: () => 'test-jwt-token' };

    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        TrackApiService,
        { provide: AuthService, useValue: authMock },
      ],
    });

    service = TestBed.inject(TrackApiService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => httpMock.verify());

  it('should fetch track history', () => {
    const mockPoints: TrackPoint[] = [{
      id: '1', entityId: 'e1', latitude: 10, longitude: 20,
      altitude: null, heading: null, speedKnots: null, course: null,
      source: null, timestamp: '2025-01-01T00:00:00Z',
    }];

    service.getHistory('e1').subscribe(points => {
      expect(points).toEqual(mockPoints);
    });

    const req = httpMock.expectOne('/api/tracks/e1');
    expect(req.request.method).toBe('GET');
    req.flush(mockPoints);
  });

  it('should fetch latest position', () => {
    const mockPoint: TrackPoint = {
      id: '1', entityId: 'e1', latitude: 10, longitude: 20,
      altitude: 500, heading: 90, speedKnots: 12, course: 180,
      source: 'AIS', timestamp: '2025-01-01T00:00:00Z',
    };

    service.getLatestPosition('e1').subscribe(point => {
      expect(point).toEqual(mockPoint);
    });

    const req = httpMock.expectOne('/api/tracks/e1/latest');
    req.flush(mockPoint);
  });

  it('should fetch segments', () => {
    service.getSegments('e1', '2025-01-01', '2025-01-02').subscribe();

    const req = httpMock.expectOne(r =>
      r.url === '/api/tracks/e1/segments' &&
      r.params.get('startTime') === '2025-01-01' &&
      r.params.get('endTime') === '2025-01-02'
    );
    expect(req.request.method).toBe('GET');
    req.flush([]);
  });
});
