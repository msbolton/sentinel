import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TrackService } from './track.service';
import { TimeWindow } from '../../shared/models/track.model';

describe('TrackService', () => {
  let service: TrackService;
  let http: HttpTestingController;

  const window: TimeWindow = {
    start: new Date('2026-11-02T10:00:00Z'),
    end: new Date('2026-11-02T11:00:00Z'),
  };
  const box = { north: 50, south: 40, east: 10, west: -5 };
  const globe = { north: 90, south: -90, east: 180, west: -180 };

  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    service = TestBed.inject(TrackService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('asks for timeline buckets with ISO times, the default bucket count and a bbox', () => {
    service.timeline(window, box).subscribe();
    const p = http.expectOne((r) => r.url === '/api/v1/tracks/timeline').request.params;
    expect(p.get('startTime')).toBe('2026-11-02T10:00:00.000Z');
    expect(p.get('endTime')).toBe('2026-11-02T11:00:00.000Z');
    expect(p.get('buckets')).toBe('120');
    expect(p.get('bbox')).toBe('-5,40,10,50');
  });

  it('honours an explicit bucket count', () => {
    service.timeline(window, box, 60).subscribe();
    expect(http.expectOne((r) => r.url === '/api/v1/tracks/timeline').request.params.get('buckets')).toBe('60');
  });

  it('omits the bbox on the timeline when there are no bounds', () => {
    service.timeline(window, null).subscribe();
    expect(http.expectOne((r) => r.url === '/api/v1/tracks/timeline').request.params.has('bbox')).toBe(false);
  });

  it('omits the bbox on the timeline for a global view', () => {
    service.timeline(window, globe).subscribe();
    expect(http.expectOne((r) => r.url === '/api/v1/tracks/timeline').request.params.has('bbox')).toBe(false);
  });

  it('asks for a replay with the bbox and the default entity cap', () => {
    service.replay(window, box).subscribe();
    const p = http.expectOne((r) => r.url === '/api/v1/tracks/replay').request.params;
    expect(p.get('startTime')).toBe('2026-11-02T10:00:00.000Z');
    expect(p.get('endTime')).toBe('2026-11-02T11:00:00.000Z');
    expect(p.get('bbox')).toBe('-5,40,10,50');
    expect(p.get('maxEntities')).toBe('500');
  });

  it('honours an explicit entity cap', () => {
    service.replay(window, box, 100).subscribe();
    expect(http.expectOne((r) => r.url === '/api/v1/tracks/replay').request.params.get('maxEntities')).toBe('100');
  });

  it('sends the whole-globe box for a global replay', () => {
    service.replay(window, globe).subscribe();
    expect(http.expectOne((r) => r.url === '/api/v1/tracks/replay').request.params.get('bbox')).toBe('-180,-90,180,90');
  });
});
