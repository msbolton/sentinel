import { readFileSync } from 'fs';
import { join } from 'path';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { Router } from '@angular/router';
import { ReplayService, ReplaySpeed, WindowPreset, FetchStatus } from '../../core/services/replay.service';
import { TimelineEvent, TimeWindow } from '../../shared/models/track.model';
import { formatDtg } from '../../shared/dtg';
import { TimelineComponent } from './timeline.component';

const HOUR = 3_600_000;
const start = new Date(Date.UTC(2026, 10, 2, 6, 0, 0));
const sixHours: TimeWindow = { start, end: new Date(start.getTime() + 6 * HOUR) };

const bucket = (count: number, entityTypes: Record<string, number>): TimelineEvent => ({
  timestamp: start.toISOString(),
  count,
  entityTypes,
});

function mockReplay() {
  return {
    mode: signal<'live' | 'replay'>('replay'),
    preset: signal<WindowPreset>('6h'),
    window: signal<TimeWindow>(sixHours),
    cursor: signal<Date>(new Date(start.getTime() + 90 * 60_000)),
    playing: signal(false),
    speed: signal<ReplaySpeed>(60),
    range: signal<TimeWindow | null>(null),
    buckets: signal<TimelineEvent[]>([
      bucket(3, { AIRCRAFT: 2, VESSEL: 1 }),
      bucket(0, {}),
      bucket(1, { DRONE: 1, VEHICLE: 0 }),
    ]),
    bucketsStatus: signal<FetchStatus>('ready'),
    replay: signal(null),
    replayStatus: signal<FetchStatus>('ready'),
    seekRequested: signal(0),
    truncated: signal(false),
    maxEntities: 500,
    enter: jest.fn(),
    exit: jest.fn(),
    setWindow: jest.fn(),
    seek: jest.fn(),
    play: jest.fn(),
    pause: jest.fn(),
    toggle: jest.fn(),
    setSpeed: jest.fn(),
    stepForward: jest.fn(),
    stepBack: jest.fn(),
    setRange: jest.fn(),
    clearRange: jest.fn(),
    refresh: jest.fn(),
  };
}

/** jsdom has no PointerEvent; a MouseEvent of the same name carries clientX and shiftKey. */
function pointer(target: Element, type: string, clientX: number, shiftKey = false): void {
  target.dispatchEvent(new MouseEvent(type, { clientX, shiftKey, bubbles: true }));
}

function key(target: Element, k: string, shiftKey = false): void {
  target.dispatchEvent(new KeyboardEvent('keydown', { key: k, shiftKey, bubbles: true }));
}

describe('TimelineComponent', () => {
  let replay: ReturnType<typeof mockReplay>;
  let navigate: jest.Mock;
  let fixture: ComponentFixture<TimelineComponent>;
  let el: HTMLElement;
  const originalResizeObserver = window.ResizeObserver;

  beforeAll(() => {
    (window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
    };
  });

  afterAll(() => {
    (window as unknown as { ResizeObserver: unknown }).ResizeObserver = originalResizeObserver;
  });

  beforeEach(() => {
    replay = mockReplay();
    navigate = jest.fn().mockResolvedValue(true);
    TestBed.configureTestingModule({
      imports: [TimelineComponent],
      providers: [
        { provide: ReplayService, useValue: replay },
        { provide: Router, useValue: { navigate } },
      ],
    });
    fixture = TestBed.createComponent(TimelineComponent);
    fixture.detectChanges();
    el = fixture.nativeElement as HTMLElement;
  });

  const strip = () => el.querySelector('.strip') as HTMLElement;
  const svg = () => el.querySelector('.histogram svg') as SVGSVGElement;
  const button = (label: string) => el.querySelector(`button[aria-label="${label}"]`) as HTMLButtonElement;

  it('enters replay on init and exits on destroy', () => {
    expect(replay.enter).toHaveBeenCalledTimes(1);
    expect(replay.exit).not.toHaveBeenCalled();
    fixture.destroy();
    expect(replay.exit).toHaveBeenCalledTimes(1);
  });

  it('makes the strip root focusable and shows the REPLAY pill', () => {
    expect(strip().getAttribute('tabindex')).toBe('0');
    expect(el.querySelector('.mode')?.textContent?.trim()).toBe('REPLAY');
  });

  it('takes pointer events although the router outlet container does not', () => {
    // .panel-overlay sets pointer-events: none and its encapsulated `> *` rule never reaches this host.
    const styles = readFileSync(join(__dirname, 'timeline.component.scss'), 'utf-8');
    expect(styles).toMatch(/^\.strip \{[^{}]*pointer-events: auto;/m);
  });

  it('renders one rect per non-zero type segment and six tick labels', () => {
    const rects = Array.from(svg().querySelectorAll('rect[data-type]'));
    expect(rects.map((r) => r.getAttribute('data-type')).sort()).toEqual(['AIRCRAFT', 'DRONE', 'VESSEL']);
    expect(rects.find((r) => r.getAttribute('data-type') === 'AIRCRAFT')?.getAttribute('fill')).toBe(
      'var(--entity-aircraft, var(--entity-unknown))',
    );
    expect(svg().querySelectorAll('text.tick')).toHaveLength(6);
    expect(svg().getAttribute('viewBox')).toBe('0 0 600 48');
    expect(svg().querySelector('line.playhead')).not.toBeNull();
  });

  it('titles each bucket with its time and total', () => {
    const titles = Array.from(svg().querySelectorAll('title')).map((t) => t.textContent);
    expect(titles[0]).toBe(`${formatDtg(start)} · 3`);
    expect(titles).toHaveLength(3);
  });

  it('places the playhead at the cursor', () => {
    // 90 minutes into a 6 h window across 600 px.
    expect(svg().querySelector('line.playhead')?.getAttribute('x1')).toBe('150');
  });

  it('seeks to the clicked time', () => {
    pointer(svg(), 'pointerdown', 300);
    pointer(svg(), 'pointerup', 300);
    expect(replay.seek).toHaveBeenCalledTimes(1);
    const t = (replay.seek.mock.calls[0][0] as Date).getTime();
    expect(Math.abs(t - (start.getTime() + 3 * HOUR))).toBeLessThan(60_000);
  });

  it('scrubs with a drag, pausing while dragging and resuming afterwards', () => {
    replay.playing.set(true);
    fixture.detectChanges();
    pointer(svg(), 'pointerdown', 150);
    pointer(svg(), 'pointermove', 200);
    pointer(svg(), 'pointermove', 450);
    expect(replay.pause).toHaveBeenCalledTimes(1);
    expect(replay.seek).toHaveBeenCalledTimes(2);
    expect((replay.seek.mock.calls[1][0] as Date).getTime()).toBe(start.getTime() + 4.5 * HOUR);
    pointer(svg(), 'pointerup', 450);
    expect(replay.play).toHaveBeenCalledTimes(1);
    expect(replay.setRange).not.toHaveBeenCalled();
  });

  it('brushes a range with Shift held away from the playhead', () => {
    pointer(svg(), 'pointerdown', 300, true);
    pointer(svg(), 'pointermove', 400, true);
    pointer(svg(), 'pointerup', 400, true);
    expect(replay.setRange).toHaveBeenLastCalledWith(
      new Date(start.getTime() + 3 * HOUR),
      new Date(start.getTime() + 4 * HOUR),
    );
    expect(replay.seek).not.toHaveBeenCalled();
  });

  it('Shift-drag from the playhead brushes', () => {
    pointer(svg(), 'pointerdown', 152, true);
    pointer(svg(), 'pointermove', 300, true);
    pointer(svg(), 'pointerup', 300, true);
    expect(replay.setRange).toHaveBeenLastCalledWith(
      new Date(start.getTime() + 1.52 * HOUR),
      new Date(start.getTime() + 3 * HOUR),
    );
    expect(replay.seek).not.toHaveBeenCalled();
    expect(replay.pause).not.toHaveBeenCalled();
  });

  it('brushes a range with a plain drag away from the playhead, earlier time first', () => {
    pointer(svg(), 'pointerdown', 450);
    pointer(svg(), 'pointermove', 300);
    pointer(svg(), 'pointerup', 300);
    expect(replay.setRange).toHaveBeenLastCalledWith(
      new Date(start.getTime() + 3 * HOUR),
      new Date(start.getTime() + 4.5 * HOUR),
    );
    expect(replay.seek).not.toHaveBeenCalled();
  });

  it('draws the range and clears it on double-click or Escape', () => {
    replay.range.set({ start: new Date(start.getTime() + HOUR), end: new Date(start.getTime() + 2 * HOUR) });
    fixture.detectChanges();
    const band = svg().querySelector('rect.range')!;
    expect(band.getAttribute('x')).toBe('100');
    expect(band.getAttribute('width')).toBe('100');
    expect(el.querySelector('.readout')?.textContent).toContain('07:00 – 08:00');

    svg().dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    expect(replay.clearRange).toHaveBeenCalledTimes(1);
    key(strip(), 'Escape');
    expect(replay.clearRange).toHaveBeenCalledTimes(2);
  });

  it('plays and pauses on Space without using toggle', () => {
    key(strip(), ' ');
    expect(replay.play).toHaveBeenCalledTimes(1);
    replay.playing.set(true);
    key(strip(), ' ');
    expect(replay.pause).toHaveBeenCalledTimes(1);
    expect(replay.toggle).not.toHaveBeenCalled();
  });

  it('steps with the arrow keys, ten at a time with Shift', () => {
    key(strip(), 'ArrowRight');
    expect(replay.stepForward).toHaveBeenLastCalledWith(1);
    key(strip(), 'ArrowRight', true);
    expect(replay.stepForward).toHaveBeenLastCalledWith(10);
    key(strip(), 'ArrowLeft');
    expect(replay.stepBack).toHaveBeenLastCalledWith(1);
    key(strip(), 'ArrowLeft', true);
    expect(replay.stepBack).toHaveBeenLastCalledWith(10);
  });

  it('goes live on l', () => {
    key(strip(), 'l');
    expect(replay.exit).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith(['/map']);
  });

  it('picks speeds with 1 to 4', () => {
    key(strip(), '1');
    expect(replay.setSpeed).toHaveBeenLastCalledWith(1);
    key(strip(), '4');
    expect(replay.setSpeed).toHaveBeenLastCalledWith(600);
  });

  it('sets the window from its button and marks the chosen one pressed', () => {
    const sevenDays = el.querySelector('button[data-preset="7d"]') as HTMLButtonElement;
    expect(sevenDays.getAttribute('aria-pressed')).toBe('false');
    expect(el.querySelector('button[data-preset="6h"]')?.getAttribute('aria-pressed')).toBe('true');
    sevenDays.click();
    expect(replay.setWindow).toHaveBeenCalledWith('7d');
    replay.preset.set('7d');
    fixture.detectChanges();
    expect(sevenDays.getAttribute('aria-pressed')).toBe('true');
  });

  it('drives the transport and speeds from their buttons', () => {
    button('Step back').click();
    expect(replay.stepBack).toHaveBeenLastCalledWith(1);
    button('Step forward').click();
    expect(replay.stepForward).toHaveBeenLastCalledWith(1);
    button('Play').click();
    expect(replay.play).toHaveBeenCalledTimes(1);
    replay.playing.set(true);
    fixture.detectChanges();
    button('Pause').click();
    expect(replay.pause).toHaveBeenCalledTimes(1);
    expect(replay.toggle).not.toHaveBeenCalled();

    const speeds = Array.from(el.querySelectorAll('.speeds button'));
    expect(speeds.map((b) => b.textContent?.trim())).toEqual(['1×', '10×', '60×', '600×']);
    expect(speeds[2].getAttribute('aria-pressed')).toBe('true');
    (speeds[1] as HTMLButtonElement).click();
    expect(replay.setSpeed).toHaveBeenCalledWith(10);
  });

  it('LIVE exits replay and returns to the map', () => {
    const live = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'LIVE')!;
    live.click();
    expect(replay.exit).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith(['/map']);
  });

  it('shows the cursor as a DTG in the readout', () => {
    expect(el.querySelector('.readout')?.textContent).toContain(formatDtg(replay.cursor()));
  });

  it('shows a loading bar while the buckets load', () => {
    expect(el.querySelector('.loading-bar')).toBeNull();
    replay.bucketsStatus.set('loading');
    fixture.detectChanges();
    expect(el.querySelector('.loading-bar')).not.toBeNull();
  });

  it('offers a retry when the buckets fail', () => {
    replay.bucketsStatus.set('failed');
    fixture.detectChanges();
    expect(el.querySelector('.histogram')?.textContent).toContain('Playback is unavailable.');
    const retry = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Retry')!;
    retry.click();
    expect(replay.refresh).toHaveBeenCalledTimes(1);
  });

  it('says so and disables the transport when the window has no points', () => {
    replay.buckets.set([bucket(0, {}), bucket(0, {})]);
    fixture.detectChanges();
    expect(el.querySelector('.histogram')?.textContent).toContain('No recorded tracks in view for this window.');
    expect(button('Play').disabled).toBe(true);
    expect(button('Step back').disabled).toBe(true);
    expect(button('Step forward').disabled).toBe(true);
    key(strip(), ' ');
    expect(replay.play).not.toHaveBeenCalled();
  });

  it('notes when the replay was truncated', () => {
    expect(el.querySelector('.note')).toBeNull();
    replay.truncated.set(true);
    fixture.detectChanges();
    expect(el.querySelector('.note')?.textContent?.trim()).toBe('Showing the 500 busiest tracks in view');
  });
});
