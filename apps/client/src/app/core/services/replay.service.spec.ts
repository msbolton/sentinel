import { signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { Observable, Subject, of, throwError } from 'rxjs';
import { ViewportBounds } from '../../features/map/viewport-bounds';
import { ReplayResponse, TimelineEvent } from '../../shared/models/track.model';
import { MapViewportService } from './map-viewport.service';
import { BUCKET_COUNT, REPLAY_SPEEDS, ReplayService, WINDOW_PRESETS } from './replay.service';
import { TrackService } from './track.service';

describe('ReplayService', () => {
  const NOW = new Date('2026-11-02T12:00:00Z');
  const HOUR = 3_600_000;
  const box: ViewportBounds = { north: 50, south: 40, east: 10, west: -5 };
  const otherBox: ViewportBounds = { north: 60, south: 55, east: 20, west: 15 };
  const response: ReplayResponse = {
    startTime: '2026-11-02T11:00:00Z',
    endTime: '2026-11-02T12:00:00Z',
    entities: [],
    truncated: true,
  };
  const buckets: TimelineEvent[] = [{ timestamp: '2026-11-02T11:00:00Z', count: 3, entityTypes: {} }];

  let bounds: ReturnType<typeof signal<ViewportBounds | null>>;
  let track: { timeline: jest.Mock; replay: jest.Mock };
  let service: ReplayService;

  function setup(): void {
    bounds = signal<ViewportBounds | null>(box);
    track = {
      timeline: jest.fn(() => of(buckets)),
      replay: jest.fn(() => of(response)),
    };
    TestBed.configureTestingModule({
      providers: [
        { provide: TrackService, useValue: track },
        { provide: MapViewportService, useValue: { bounds } },
      ],
    });
    service = TestBed.inject(ReplayService);
    TestBed.flushEffects();
  }

  /** Let the 500 ms refresh debounce elapse. */
  function settle(): void {
    jest.advanceTimersByTime(500);
  }

  beforeEach(() => {
    jest.useFakeTimers();
    jest.setSystemTime(NOW);
    setup();
  });

  afterEach(() => jest.useRealTimers());

  it('offers the four window presets and four speeds', () => {
    expect(WINDOW_PRESETS).toEqual({ '1h': HOUR, '6h': 6 * HOUR, '24h': 24 * HOUR, '7d': 168 * HOUR });
    expect(REPLAY_SPEEDS).toEqual([1, 10, 60, 600]);
    expect(BUCKET_COUNT).toBe(120);
  });

  it('starts live, paused and empty', () => {
    expect(service.mode()).toBe('live');
    expect(service.playing()).toBe(false);
    expect(service.replay()).toBeNull();
    expect(service.buckets()).toEqual([]);
    expect(service.bucketsStatus()).toBe('idle');
    expect(service.replayStatus()).toBe('idle');
    expect(service.truncated()).toBe(false);
  });

  describe('enter', () => {
    it('picks the 1h window ending now with the cursor at its start', () => {
      service.enter();

      expect(service.mode()).toBe('replay');
      expect(service.preset()).toBe('1h');
      expect(service.window().end).toEqual(NOW);
      expect(service.window().start).toEqual(new Date(NOW.getTime() - HOUR));
      expect(service.cursor()).toEqual(service.window().start);
      expect(service.stepMs()).toBe(HOUR / BUCKET_COUNT);
    });

    it('fetches buckets and replay once, after the debounce, with the viewport bounds', () => {
      service.enter();
      expect(track.timeline).not.toHaveBeenCalled();

      jest.advanceTimersByTime(499);
      expect(track.timeline).not.toHaveBeenCalled();
      jest.advanceTimersByTime(1);

      expect(track.timeline).toHaveBeenCalledTimes(1);
      expect(track.timeline).toHaveBeenCalledWith(service.window(), box, BUCKET_COUNT);
      expect(track.replay).toHaveBeenCalledTimes(1);
      expect(track.replay).toHaveBeenCalledWith(service.window(), box, service.maxEntities);
      expect(service.maxEntities).toBe(500);
      expect(service.buckets()).toEqual(buckets);
      expect(service.replay()).toEqual(response);
      expect(service.truncated()).toBe(true);
      expect(service.bucketsStatus()).toBe('ready');
      expect(service.replayStatus()).toBe('ready');
    });

    it('reports loading while the requests are in flight', () => {
      const pending = new Subject<ReplayResponse>();
      track.replay.mockReturnValue(pending);
      service.enter();
      settle();

      expect(service.replayStatus()).toBe('loading');
      expect(service.bucketsStatus()).toBe('ready');
      pending.next(response);
      expect(service.replayStatus()).toBe('ready');
    });
  });

  describe('setWindow', () => {
    it('gives a 7-day window, clears the range and refetches', () => {
      service.enter();
      settle();
      service.setRange(
        new Date(NOW.getTime() - HOUR / 2),
        new Date(NOW.getTime() - HOUR / 4),
      );
      track.timeline.mockClear();

      service.setWindow('7d');

      expect(service.preset()).toBe('7d');
      expect(service.window().end).toEqual(new Date()); // re-ended at the (faked) present
      expect(service.window().end.getTime() - service.window().start.getTime()).toBe(168 * HOUR);
      expect(service.range()).toBeNull();
      expect(service.cursor()).toEqual(service.window().start);
      settle();
      expect(track.timeline).toHaveBeenCalledTimes(1);
      expect(track.timeline).toHaveBeenCalledWith(service.window(), box, BUCKET_COUNT);
    });

    it('re-ends the window at the current time', () => {
      service.enter();
      jest.setSystemTime(new Date(NOW.getTime() + 10 * 60_000));
      service.setWindow('1h');
      expect(service.window().end).toEqual(new Date(NOW.getTime() + 10 * 60_000));
    });
  });

  describe('seek and tick', () => {
    beforeEach(() => service.enter());

    it('seek clamps to the window', () => {
      const { start, end } = service.window();
      service.seek(new Date(start.getTime() - HOUR));
      expect(service.cursor()).toEqual(start);
      service.seek(new Date(end.getTime() + HOUR));
      expect(service.cursor()).toEqual(end);
      const mid = new Date(start.getTime() + HOUR / 2);
      service.seek(mid);
      expect(service.cursor()).toEqual(mid);
    });

    it('tick past the window end pauses at the end', () => {
      service.play();
      service.tick(new Date(service.window().end.getTime() + 1000));

      expect(service.cursor()).toEqual(service.window().end);
      expect(service.playing()).toBe(false);
    });

    it('tick exactly at the window end pauses there (a clamped clock never passes it)', () => {
      service.play();
      service.tick(service.window().end);

      expect(service.cursor()).toEqual(service.window().end);
      expect(service.playing()).toBe(false);
    });

    it('tick inside the window just moves the cursor and keeps playing', () => {
      service.play();
      const t = new Date(service.window().start.getTime() + 5000);
      service.tick(t);
      expect(service.cursor()).toEqual(t);
      expect(service.playing()).toBe(true);
    });

    it('with a range, tick past its end loops to its start and keeps playing', () => {
      const a = new Date(service.window().start.getTime() + 10 * 60_000);
      const b = new Date(service.window().start.getTime() + 20 * 60_000);
      service.setRange(a, b);
      service.seekApplied(); // what the map does once its clock is at the range start
      service.play();

      service.tick(new Date(b.getTime() + 1000));

      expect(service.cursor()).toEqual(a);
      expect(service.playing()).toBe(true);
    });

    it('with a range, tick exactly at its end loops to its start and keeps playing', () => {
      const a = new Date(service.window().start.getTime() + 10 * 60_000);
      const b = new Date(service.window().start.getTime() + 20 * 60_000);
      service.setRange(a, b);
      service.seekApplied(); // what the map does once its clock is at the range start
      service.play();

      service.tick(b);

      expect(service.cursor()).toEqual(a);
      expect(service.playing()).toBe(true);
    });

    it('with a range, seek clamps to the range', () => {
      const a = new Date(service.window().start.getTime() + 10 * 60_000);
      const b = new Date(service.window().start.getTime() + 20 * 60_000);
      service.setRange(a, b);

      service.seek(service.window().start);
      expect(service.cursor()).toEqual(a);
      service.seek(service.window().end);
      expect(service.cursor()).toEqual(b);
    });

    it('seek increments seekRequested but tick does not', () => {
      const before = service.seekRequested().n;
      service.tick(new Date(service.window().start.getTime() + 1000));
      expect(service.seekRequested().n).toBe(before);

      service.seek(new Date(service.window().start.getTime() + 2000));
      expect(service.seekRequested().n).toBe(before + 1);
    });

    it('carries the seek target with the request', () => {
      const t = new Date(service.window().start.getTime() + 20 * 60_000);
      service.seek(t);
      expect(service.seekRequested().at).toEqual(t);

      service.stepForward();
      expect(service.seekRequested().at).toEqual(new Date(t.getTime() + service.stepMs()));

      const a = new Date(service.window().start.getTime() + 40 * 60_000);
      const b = new Date(service.window().start.getTime() + 50 * 60_000);
      service.setRange(a, b);
      expect(service.seekRequested().at).toEqual(a);

      service.setWindow('6h');
      expect(service.seekRequested().at).toEqual(service.window().start);
    });

    it('a tick arriving after a seek but before the map applies it does not move the cursor', () => {
      service.play();
      const target = new Date(service.window().start.getTime() + 30 * 60_000);
      service.seek(target);

      // The clock was still at its old position when it reported.
      service.tick(new Date(service.window().start.getTime() + 5000));
      expect(service.cursor()).toEqual(target);
      expect(service.playing()).toBe(true);

      service.seekApplied();
      const later = new Date(target.getTime() + 1000);
      service.tick(later);
      expect(service.cursor()).toEqual(later);
    });

    it('a stale tick past the end cannot pause a seek that is still pending', () => {
      service.play();
      const target = new Date(service.window().start.getTime() + 30 * 60_000);
      service.seek(target);
      service.tick(service.window().end);
      expect(service.playing()).toBe(true);
      expect(service.cursor()).toEqual(target);
    });

    it('exit drops a pending seek', () => {
      service.seek(new Date(service.window().start.getTime() + 30 * 60_000));
      service.exit();
      service.enter();
      const t = new Date(service.window().start.getTime() + 1000);
      service.tick(t);
      expect(service.cursor()).toEqual(t);
    });
  });

  describe('setRange and clearRange', () => {
    beforeEach(() => service.enter());

    it('orders the two ends, moves the cursor into the range and marks a seek', () => {
      const start = service.window().start.getTime();
      const a = new Date(start + 30 * 60_000);
      const b = new Date(start + 10 * 60_000);
      const before = service.seekRequested().n;

      service.setRange(a, b);

      expect(service.range()).toEqual({ start: b, end: a });
      expect(service.clockBounds()).toEqual({ start: b, end: a });
      expect(service.cursor()).toEqual(b);
      expect(service.seekRequested().n).toBe(before + 1);
    });

    it('keeps a cursor that is already inside the range', () => {
      const start = service.window().start.getTime();
      service.seek(new Date(start + 15 * 60_000));
      service.setRange(new Date(start + 10 * 60_000), new Date(start + 20 * 60_000));
      expect(service.cursor()).toEqual(new Date(start + 15 * 60_000));
    });

    it('clearRange goes back to the whole window', () => {
      const start = service.window().start.getTime();
      service.setRange(new Date(start + 10 * 60_000), new Date(start + 20 * 60_000));
      service.clearRange();
      expect(service.range()).toBeNull();
      expect(service.clockBounds()).toEqual(service.window());
    });
  });

  describe('stepping', () => {
    beforeEach(() => service.enter());

    it('stepForward moves one step by default', () => {
      const start = service.window().start.getTime();
      service.stepForward();
      expect(service.cursor().getTime()).toBe(start + service.stepMs());
    });

    it('stepBack(10) moves ten steps', () => {
      const start = service.window().start.getTime();
      service.seek(new Date(start + 30 * service.stepMs()));
      service.stepBack(10);
      expect(service.cursor().getTime()).toBe(start + 20 * service.stepMs());
    });

    it('stepping clamps at the window and marks one seek', () => {
      const before = service.seekRequested().n;
      service.stepBack(5);
      expect(service.cursor()).toEqual(service.window().start);
      expect(service.seekRequested().n).toBe(before + 1);
    });
  });

  describe('transport', () => {
    it('play, pause and toggle flip playing, setSpeed sets the speed', () => {
      expect(service.speed()).toBe(60);
      service.play();
      expect(service.playing()).toBe(true);
      service.pause();
      expect(service.playing()).toBe(false);
      service.toggle();
      expect(service.playing()).toBe(true);
      service.toggle();
      expect(service.playing()).toBe(false);
      service.setSpeed(600);
      expect(service.speed()).toBe(600);
    });

    it('play from the end of the window rewinds to the start and marks a seek', () => {
      service.enter();
      service.seek(service.window().end);
      const before = service.seekRequested().n;

      service.play();

      expect(service.cursor()).toEqual(service.window().start);
      expect(service.playing()).toBe(true);
      expect(service.seekRequested().n).toBe(before + 1);
    });

    it('play from the end of a range rewinds to the range start', () => {
      service.enter();
      const start = service.window().start.getTime();
      const a = new Date(start + 10 * 60_000);
      const b = new Date(start + 20 * 60_000);
      service.setRange(a, b);
      service.seek(b);

      service.play();

      expect(service.cursor()).toEqual(a);
      expect(service.playing()).toBe(true);
    });

    it('play mid-window keeps the cursor and does not mark a seek', () => {
      service.enter();
      const mid = new Date(service.window().start.getTime() + 1000);
      service.seek(mid);
      const before = service.seekRequested().n;

      service.play();

      expect(service.cursor()).toEqual(mid);
      expect(service.seekRequested().n).toBe(before);
    });
  });

  describe('bounds changes', () => {
    it('refetch after the debounce while in replay, once for a burst', () => {
      service.enter();
      settle();
      track.timeline.mockClear();
      track.replay.mockClear();

      bounds.set(otherBox);
      TestBed.flushEffects();
      jest.advanceTimersByTime(200);
      bounds.set(box);
      TestBed.flushEffects();
      bounds.set(otherBox);
      TestBed.flushEffects();
      settle();

      expect(track.timeline).toHaveBeenCalledTimes(1);
      expect(track.timeline).toHaveBeenCalledWith(service.window(), otherBox, BUCKET_COUNT);
      expect(track.replay).toHaveBeenCalledTimes(1);
      expect(track.replay).toHaveBeenCalledWith(service.window(), otherBox, 500);
    });

    it('do nothing while live', () => {
      bounds.set(otherBox);
      TestBed.flushEffects();
      settle();
      expect(track.timeline).not.toHaveBeenCalled();
      expect(track.replay).not.toHaveBeenCalled();
    });

    it('fall back to the whole globe for replay when the map has not reported a view', () => {
      bounds.set(null);
      TestBed.flushEffects();
      service.enter();
      settle();

      expect(track.timeline).toHaveBeenCalledWith(service.window(), null, BUCKET_COUNT);
      expect(track.replay).toHaveBeenCalledWith(
        service.window(),
        { north: 90, south: -90, east: 180, west: -180 },
        500,
      );
    });
  });

  describe('exit', () => {
    it('sets live, pauses and clears the data', () => {
      service.enter();
      settle();
      service.play();

      service.exit();

      expect(service.mode()).toBe('live');
      expect(service.playing()).toBe(false);
      expect(service.replay()).toBeNull();
      expect(service.buckets()).toEqual([]);
      expect(service.bucketsStatus()).toBe('idle');
      expect(service.replayStatus()).toBe('idle');
    });

    it('ignores a response that arrives after it', () => {
      const pendingReplay = new Subject<ReplayResponse>();
      const pendingBuckets = new Subject<TimelineEvent[]>();
      track.replay.mockReturnValue(pendingReplay);
      track.timeline.mockReturnValue(pendingBuckets);
      service.enter();
      settle();

      service.exit();
      pendingReplay.next(response);
      pendingBuckets.next(buckets);

      expect(service.replay()).toBeNull();
      expect(service.buckets()).toEqual([]);
      expect(pendingReplay.observed).toBe(false);
    });

    it('cancels a refresh that is still waiting on the debounce', () => {
      service.enter();
      service.exit();
      settle();
      expect(track.timeline).not.toHaveBeenCalled();
      expect(track.replay).not.toHaveBeenCalled();
    });
  });

  describe('refresh', () => {
    it('a newer refresh supersedes a request still in flight', () => {
      const first = new Subject<ReplayResponse>();
      track.replay.mockReturnValueOnce(first);
      service.enter();
      settle();

      service.refresh();
      settle();
      first.next({ ...response, truncated: false, entities: [] });

      expect(first.observed).toBe(false);
      expect(service.replay()).toEqual(response);
    });
  });

  describe('failures', () => {
    it('a failing timeline marks the buckets failed and leaves the replay alone', () => {
      track.timeline.mockReturnValue(throwError(() => new Error('boom')));
      service.enter();
      settle();

      expect(service.bucketsStatus()).toBe('failed');
      expect(service.replayStatus()).toBe('ready');
      expect(service.replay()).toEqual(response);
    });

    it('a failing replay marks the replay failed and clears the previous response, so the map draws nothing stale', () => {
      service.enter();
      settle();
      expect(service.replay()).toEqual(response);
      track.replay.mockReturnValue(throwError(() => new Error('boom')) as Observable<ReplayResponse>);

      service.refresh();
      settle();

      expect(service.replayStatus()).toBe('failed');
      expect(service.replay()).toBeNull();
      expect(service.truncated()).toBe(false);
      expect(service.bucketsStatus()).toBe('ready');
      expect(service.buckets()).toEqual(buckets);
    });

    it('a failing timeline keeps the previous buckets', () => {
      service.enter();
      settle();
      track.timeline.mockReturnValue(throwError(() => new Error('boom')) as Observable<TimelineEvent[]>);

      service.refresh();
      settle();

      expect(service.bucketsStatus()).toBe('failed');
      expect(service.buckets()).toEqual(buckets);
    });
  });
});
