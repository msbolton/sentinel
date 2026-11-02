import { DestroyRef, Injectable, computed, effect, inject, signal, untracked } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { EMPTY, Subject, catchError, debounceTime, merge, switchMap, takeUntil, tap } from 'rxjs';
import { GLOBAL_BOUNDS } from '../../features/map/viewport-bounds';
import { ReplayResponse, TimeWindow, TimelineEvent } from '../../shared/models/track.model';
import { MapViewportService } from './map-viewport.service';
import { TrackService } from './track.service';

export type WindowPreset = '1h' | '6h' | '24h' | '7d';

const HOUR_MS = 3_600_000;

/** Window lengths in milliseconds. */
export const WINDOW_PRESETS: Record<WindowPreset, number> = {
  '1h': HOUR_MS,
  '6h': 6 * HOUR_MS,
  '24h': 24 * HOUR_MS,
  '7d': 168 * HOUR_MS,
};

export type ReplaySpeed = 1 | 10 | 60 | 600;

/** Playback rates, in simulated seconds per wall-clock second. */
export const REPLAY_SPEEDS: readonly ReplaySpeed[] = [1, 10, 60, 600];

/** Timeline buckets per window; one step of the transport is one bucket. */
export const BUCKET_COUNT = 120;

export type FetchStatus = 'idle' | 'loading' | 'ready' | 'failed';

export type ReplayMode = 'live' | 'replay';

/** How long viewport and window changes settle before the history is refetched. */
const REFRESH_DEBOUNCE_MS = 500;

/**
 * The single owner of playback time. The timeline strip writes the window,
 * range, cursor and transport state here; the map reads the signals to drive
 * its clock and reports clock ticks back through {@link tick}.
 */
@Injectable({ providedIn: 'root' })
export class ReplayService {
  private readonly tracks = inject(TrackService);
  private readonly viewport = inject(MapViewportService);

  private readonly modeState = signal<ReplayMode>('live');
  private readonly presetState = signal<WindowPreset>('1h');
  private readonly windowState = signal<TimeWindow>(this.windowEndingNow('1h'));
  private readonly cursorState = signal<Date>(this.windowState().start);
  private readonly playingState = signal(false);
  private readonly speedState = signal<ReplaySpeed>(60);
  private readonly rangeState = signal<TimeWindow | null>(null);
  private readonly bucketsState = signal<TimelineEvent[]>([]);
  private readonly bucketsStatusState = signal<FetchStatus>('idle');
  private readonly replayState = signal<ReplayResponse | null>(null);
  private readonly replayStatusState = signal<FetchStatus>('idle');
  private readonly seekRequestedState = signal(0);

  readonly mode = this.modeState.asReadonly();
  readonly preset = this.presetState.asReadonly();
  readonly window = this.windowState.asReadonly();
  readonly cursor = this.cursorState.asReadonly();
  readonly playing = this.playingState.asReadonly();
  readonly speed = this.speedState.asReadonly();
  /** The sub-range playback loops over, or null to play the whole window. */
  readonly range = this.rangeState.asReadonly();
  readonly buckets = this.bucketsState.asReadonly();
  readonly bucketsStatus = this.bucketsStatusState.asReadonly();
  readonly replay = this.replayState.asReadonly();
  readonly replayStatus = this.replayStatusState.asReadonly();
  /** Increments on every requested jump (never on {@link tick}) so the map can tell a seek from its own clock. */
  readonly seekRequested = this.seekRequestedState.asReadonly();

  /** The most entities one replay fetch returns; the strip says so when `truncated`. */
  readonly maxEntities = 500;

  readonly truncated = computed(() => this.replayState()?.truncated ?? false);
  /** One transport step: a single timeline bucket. */
  readonly stepMs = computed(() => {
    const w = this.windowState();
    return (w.end.getTime() - w.start.getTime()) / BUCKET_COUNT;
  });
  /** What the clock plays over and the cursor is confined to. */
  readonly clockBounds = computed(() => this.rangeState() ?? this.windowState());

  private readonly refreshRequests = new Subject<void>();
  private readonly exits = new Subject<void>();

  constructor() {
    this.refreshRequests
      .pipe(
        debounceTime(REFRESH_DEBOUNCE_MS),
        switchMap(() => (this.modeState() === 'replay' ? this.fetch() : EMPTY)),
        takeUntilDestroyed(inject(DestroyRef)),
      )
      .subscribe();

    effect(() => {
      this.viewport.bounds();
      if (untracked(this.modeState) === 'replay') this.refresh();
    });
  }

  enter(): void {
    this.modeState.set('replay');
    this.resetWindow(this.presetState());
    this.refresh();
  }

  exit(): void {
    this.exits.next();
    this.modeState.set('live');
    this.playingState.set(false);
    this.replayState.set(null);
    this.bucketsState.set([]);
    this.replayStatusState.set('idle');
    this.bucketsStatusState.set('idle');
  }

  setWindow(preset: WindowPreset): void {
    this.presetState.set(preset);
    this.resetWindow(preset);
    this.seekRequestedState.update((n) => n + 1);
    this.refresh();
  }

  seek(d: Date): void {
    this.cursorState.set(this.clamped(d.getTime()));
    this.seekRequestedState.update((n) => n + 1);
  }

  play(): void {
    this.playingState.set(true);
  }

  pause(): void {
    this.playingState.set(false);
  }

  toggle(): void {
    this.playingState.update((p) => !p);
  }

  setSpeed(speed: ReplaySpeed): void {
    this.speedState.set(speed);
  }

  stepForward(n = 1): void {
    this.seek(new Date(this.cursorState().getTime() + n * this.stepMs()));
  }

  stepBack(n = 1): void {
    this.stepForward(-n);
  }

  /** Confine playback to the span between two instants, given in either order. */
  setRange(a: Date, b: Date): void {
    const w = this.windowState();
    const lo = Math.max(w.start.getTime(), Math.min(a.getTime(), b.getTime()));
    const hi = Math.min(w.end.getTime(), Math.max(a.getTime(), b.getTime()));
    this.rangeState.set({ start: new Date(lo), end: new Date(hi) });
    this.cursorState.set(this.clamped(this.cursorState().getTime()));
    this.seekRequestedState.update((n) => n + 1);
  }

  clearRange(): void {
    this.rangeState.set(null);
  }

  /** Refetch buckets and replay for the current window and viewport, after a short debounce. */
  refresh(): void {
    this.refreshRequests.next();
  }

  /** The map's clock reports its time here; loops over a range, stops at the end of a bare window. */
  tick(d: Date): void {
    const { start, end } = this.clockBounds();
    const t = d.getTime();
    if (t <= end.getTime()) {
      this.cursorState.set(this.clamped(t));
    } else if (this.rangeState()) {
      this.cursorState.set(start);
    } else {
      this.cursorState.set(end);
      this.playingState.set(false);
    }
  }

  private resetWindow(preset: WindowPreset): void {
    const w = this.windowEndingNow(preset);
    this.windowState.set(w);
    this.rangeState.set(null);
    this.cursorState.set(w.start);
  }

  private windowEndingNow(preset: WindowPreset): TimeWindow {
    const end = new Date();
    return { start: new Date(end.getTime() - WINDOW_PRESETS[preset]), end };
  }

  private clamped(t: number): Date {
    const { start, end } = this.clockBounds();
    return new Date(Math.min(end.getTime(), Math.max(start.getTime(), t)));
  }

  /** Both requests for the current window and view; a newer fetch or an exit abandons them. */
  private fetch() {
    const window = this.windowState();
    const bounds = this.viewport.bounds();
    this.bucketsStatusState.set('loading');
    this.replayStatusState.set('loading');

    const buckets$ = this.tracks.timeline(window, bounds, BUCKET_COUNT).pipe(
      tap((events) => {
        this.bucketsState.set(events);
        this.bucketsStatusState.set('ready');
      }),
      catchError(() => {
        this.bucketsStatusState.set('failed');
        return EMPTY;
      }),
    );
    const replay$ = this.tracks.replay(window, bounds ?? GLOBAL_BOUNDS, this.maxEntities).pipe(
      tap((response) => {
        this.replayState.set(response);
        this.replayStatusState.set('ready');
      }),
      catchError(() => {
        this.replayStatusState.set('failed');
        return EMPTY;
      }),
    );
    return merge(buckets$, replay$).pipe(takeUntil(this.exits));
  }
}
