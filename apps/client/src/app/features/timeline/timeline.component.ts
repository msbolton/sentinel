import {
  AfterViewInit,
  ChangeDetectionStrategy,
  Component,
  ElementRef,
  OnDestroy,
  OnInit,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { Router } from '@angular/router';
import {
  REPLAY_SPEEDS,
  ReplayService,
  WINDOW_PRESETS,
  WindowPreset,
} from '../../core/services/replay.service';
import { IconComponent } from '../../shared/components/icon.component';
import { formatDtg } from '../../shared/dtg';
import { stackBuckets, tickLabels, timeToX, typeColorVar, xToTime } from './timeline-scale';

/** The svg's drawing height; bars sit above the baseline and tick labels below it. */
const SVG_HEIGHT = 48;
const BAR_HEIGHT = 34;
const TICK_Y = 46;
/** Used until the ResizeObserver reports a width (and always in jsdom). */
const FALLBACK_WIDTH = 600;
/** A press this close to the playhead grabs it to scrub; Shift, or a press elsewhere, brushes a range. */
const PLAYHEAD_GRAB_PX = 6;
/** Movement below this is still a click. */
const DRAG_THRESHOLD_PX = 3;

interface Drag {
  kind: 'scrub' | 'brush';
  startX: number;
  anchor: Date;
  moved: boolean;
  /** Playback was running when the scrub began and resumes when it ends. */
  resume: boolean;
}

const pad2 = (n: number): string => String(n).padStart(2, '0');
const hhmm = (d: Date): string => `${pad2(d.getUTCHours())}:${pad2(d.getUTCMinutes())}`;

/**
 * The replay strip along the bottom of the map. Opening it puts the map into
 * replay; it reads and drives {@link ReplayService} and holds no time of its own.
 */
@Component({
  selector: 'app-timeline',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [IconComponent],
  templateUrl: './timeline.component.html',
  styleUrls: ['./timeline.component.scss'],
})
export class TimelineComponent implements OnInit, AfterViewInit, OnDestroy {
  protected readonly replay = inject(ReplayService);
  private readonly router = inject(Router);

  private readonly histogram = viewChild.required<ElementRef<HTMLElement>>('histogram');
  private resizeObserver: ResizeObserver | null = null;
  private drag: Drag | null = null;

  protected readonly presets = Object.keys(WINDOW_PRESETS) as WindowPreset[];
  protected readonly speeds = REPLAY_SPEEDS;
  protected readonly svgHeight = SVG_HEIGHT;
  protected readonly barHeight = BAR_HEIGHT;
  protected readonly tickY = TICK_Y;
  protected readonly typeColorVar = typeColorVar;

  protected readonly width = signal(FALLBACK_WIDTH);

  protected readonly columns = computed(() => {
    const buckets = this.replay.buckets();
    return stackBuckets(buckets, BAR_HEIGHT, this.width()).map((bar, i) => ({
      ...bar,
      drawWidth: bar.width > 2 ? bar.width - 1 : bar.width,
      title: `${formatDtg(new Date(buckets[i].timestamp))} · ${buckets[i].count}`,
    }));
  });

  protected readonly ticks = computed(() => {
    const width = this.width();
    const labels = tickLabels(this.replay.window());
    return labels.map((t, i) => ({
      x: t.x * width,
      label: t.label,
      anchor: i === 0 ? 'start' : i === labels.length - 1 ? 'end' : 'middle',
    }));
  });

  protected readonly playheadX = computed(() => timeToX(this.replay.cursor(), this.replay.window(), this.width()));
  protected readonly capPoints = computed(() => {
    const x = this.playheadX();
    return `${x - 3},0 ${x + 3},0 ${x},5`;
  });

  protected readonly rangeBand = computed(() => {
    const range = this.replay.range();
    if (!range) return null;
    const x = timeToX(range.start, this.replay.window(), this.width());
    return { x, width: timeToX(range.end, this.replay.window(), this.width()) - x };
  });

  /** Either request in flight: the histogram and the replay arrive separately. */
  protected readonly loading = computed(
    () => this.replay.bucketsStatus() === 'loading' || this.replay.replayStatus() === 'loading',
  );
  /** Either request failed: there is nothing trustworthy to play until a retry succeeds. */
  protected readonly failed = computed(
    () => this.replay.bucketsStatus() === 'failed' || this.replay.replayStatus() === 'failed',
  );
  protected readonly empty = computed(
    () => this.replay.bucketsStatus() === 'ready' && this.replay.buckets().every((b) => b.count === 0),
  );
  protected readonly transportDisabled = computed(() => this.empty() || this.failed());

  protected readonly dtg = computed(() => formatDtg(this.replay.cursor()));
  protected readonly rangeText = computed(() => {
    const range = this.replay.range();
    return range ? `${hhmm(range.start)} – ${hhmm(range.end)}` : null;
  });

  ngOnInit(): void {
    this.replay.enter();
  }

  ngAfterViewInit(): void {
    if (typeof ResizeObserver === 'undefined') return;
    this.resizeObserver = new ResizeObserver(([entry]) => {
      const width = Math.round(entry.contentRect.width);
      if (width > 0) this.width.set(width);
    });
    this.resizeObserver.observe(this.histogram().nativeElement);
  }

  ngOnDestroy(): void {
    this.resizeObserver?.disconnect();
    this.replay.exit();
  }

  protected playPause(): void {
    if (this.transportDisabled()) return;
    // Never toggle(): play() rewinds from the end of the bounds, toggle() does not.
    if (this.replay.playing()) this.replay.pause();
    else this.replay.play();
  }

  protected step(n: number): void {
    if (this.transportDisabled()) return;
    if (n > 0) this.replay.stepForward(n);
    else this.replay.stepBack(-n);
  }

  protected goLive(): void {
    this.replay.exit();
    void this.router.navigate(['/map']);
  }

  protected onKeydown(e: KeyboardEvent): void {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    switch (e.key) {
      case ' ':
        // A focused button activates itself on Space.
        if (e.target instanceof HTMLButtonElement) return;
        this.playPause();
        break;
      case 'ArrowLeft':
        this.step(e.shiftKey ? -10 : -1);
        break;
      case 'ArrowRight':
        this.step(e.shiftKey ? 10 : 1);
        break;
      case 'Escape':
        this.replay.clearRange();
        break;
      case 'l':
      case 'L':
        this.goLive();
        break;
      case '1':
      case '2':
      case '3':
      case '4':
        this.replay.setSpeed(REPLAY_SPEEDS[Number(e.key) - 1]);
        break;
      default:
        return;
    }
    e.preventDefault();
  }

  protected onPointerDown(e: PointerEvent): void {
    if (e.button !== 0) return;
    const x = this.localX(e);
    const grabsPlayhead = Math.abs(x - this.playheadX()) <= PLAYHEAD_GRAB_PX;
    this.drag = {
      kind: grabsPlayhead && !e.shiftKey ? 'scrub' : 'brush',
      startX: x,
      anchor: this.timeAt(x),
      moved: false,
      resume: false,
    };
    const target = e.currentTarget as Element;
    if (e.pointerId !== undefined) target.setPointerCapture?.(e.pointerId);
  }

  protected onPointerMove(e: PointerEvent): void {
    const drag = this.drag;
    if (!drag) return;
    const x = this.localX(e);
    if (!drag.moved) {
      if (Math.abs(x - drag.startX) < DRAG_THRESHOLD_PX) return;
      drag.moved = true;
      if (drag.kind === 'scrub' && this.replay.playing()) {
        drag.resume = true;
        this.replay.pause();
      }
    }
    if (drag.kind === 'scrub') this.replay.seek(this.timeAt(x));
    else {
      const t = this.timeAt(x);
      if (t < drag.anchor) this.replay.setRange(t, drag.anchor);
      else this.replay.setRange(drag.anchor, t);
    }
  }

  protected onPointerUp(e: PointerEvent): void {
    const drag = this.drag;
    if (!drag) return;
    this.drag = null;
    if (!drag.moved) this.replay.seek(this.timeAt(this.localX(e)));
    else if (drag.resume) this.replay.play();
  }

  protected onPointerCancel(): void {
    if (this.drag?.resume) this.replay.play();
    this.drag = null;
  }

  private localX(e: MouseEvent): number {
    const svg = e.currentTarget as Element;
    return e.clientX - svg.getBoundingClientRect().left;
  }

  private timeAt(x: number): Date {
    return xToTime(x, this.replay.window(), this.width());
  }
}
