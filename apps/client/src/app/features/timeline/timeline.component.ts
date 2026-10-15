import {
  Component,
  OnInit,
  OnDestroy,
  AfterViewInit,
  ViewChild,
  ElementRef,
  signal,
  NgZone,
  ChangeDetectionStrategy,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Subscription, interval } from 'rxjs';
import { PlaybackState, TimelineEvent } from '../../shared/models/track.model';

@Component({
  selector: 'app-timeline',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule],
  templateUrl: './timeline.component.html',
  styleUrls: ['./timeline.component.scss'],
})
export class TimelineComponent implements OnInit, OnDestroy, AfterViewInit {
  @ViewChild('timelineCanvas', { static: true })
  timelineCanvas!: ElementRef<HTMLCanvasElement>;

  @ViewChild('brushOverlay', { static: true })
  brushOverlay!: ElementRef<HTMLDivElement>;

  playing = signal<boolean>(false);
  speed = signal<number>(1);
  currentTime = signal<Date>(new Date());
  startTime = signal<Date>(new Date(Date.now() - 24 * 60 * 60 * 1000)); // 24h ago
  endTime = signal<Date>(new Date());
  brushStart = signal<Date | null>(null);
  brushEnd = signal<Date | null>(null);
  entityCountAtTime = signal<number>(0);
  timelineEvents = signal<TimelineEvent[]>([]);

  speeds = [0.5, 1, 2, 5, 10, 30, 60];

  private playbackSubscription: Subscription | null = null;
  private subscriptions = new Subscription();
  private d3: any = null;
  private canvasCtx: CanvasRenderingContext2D | null = null;
  private isDragging = false;
  private dragStartX = 0;

  constructor(
    private readonly ngZone: NgZone,
    private readonly http: HttpClient,
  ) {}

  ngOnInit(): void {
    this.loadTimelineData();
  }

  async ngAfterViewInit(): Promise<void> {
    this.setupCanvas();
    this.setupBrushInteraction();
    this.renderTimeline();
  }

  ngOnDestroy(): void {
    this.pause();
    this.subscriptions.unsubscribe();
  }

  private setupCanvas(): void {
    const canvas = this.timelineCanvas.nativeElement;
    const rect = canvas.parentElement?.getBoundingClientRect();
    if (rect) {
      canvas.width = rect.width;
      canvas.height = rect.height;
    }
    this.canvasCtx = canvas.getContext('2d');
  }

  private setupBrushInteraction(): void {
    const overlay = this.brushOverlay.nativeElement;

    overlay.addEventListener('mousedown', (e: MouseEvent) => {
      this.isDragging = true;
      this.dragStartX = e.offsetX;
      this.brushStart.set(this.xToTime(e.offsetX));
      this.brushEnd.set(null);
    });

    overlay.addEventListener('mousemove', (e: MouseEvent) => {
      if (!this.isDragging) return;
      this.brushEnd.set(this.xToTime(e.offsetX));
      this.renderTimeline();
    });

    overlay.addEventListener('mouseup', () => {
      this.isDragging = false;
      if (this.brushStart() && this.brushEnd()) {
        // Ensure start < end
        const s = this.brushStart()!;
        const e = this.brushEnd()!;
        if (s > e) {
          this.brushStart.set(e);
          this.brushEnd.set(s);
        }
      }
    });

    overlay.addEventListener('dblclick', () => {
      // Clear brush
      this.brushStart.set(null);
      this.brushEnd.set(null);
      this.renderTimeline();
    });
  }

  private loadTimelineData(): void {
    const params = new HttpParams()
      .set('startTime', this.startTime().toISOString())
      .set('endTime', this.endTime().toISOString())
      .set('buckets', '100');

    this.http
      .get<TimelineEvent[]>('/api/v1/tracks/timeline', { params })
      .subscribe({
        next: (events) => {
          this.timelineEvents.set(events);
          this.renderTimeline();
        },
        error: () => {
          // Generate placeholder data for visualization
          this.generatePlaceholderData();
          this.renderTimeline();
        },
      });
  }

  private generatePlaceholderData(): void {
    const events: TimelineEvent[] = [];
    const start = this.startTime().getTime();
    const end = this.endTime().getTime();
    const step = (end - start) / 100;

    for (let i = 0; i < 100; i++) {
      const t = start + i * step;
      events.push({
        timestamp: new Date(t).toISOString(),
        count: Math.floor(Math.random() * 50 + 10),
        entityTypes: {
          PERSON: Math.floor(Math.random() * 15),
          VEHICLE: Math.floor(Math.random() * 10),
          VESSEL: Math.floor(Math.random() * 8),
          AIRCRAFT: Math.floor(Math.random() * 5),
          FACILITY: Math.floor(Math.random() * 3),
        },
      });
    }
    this.timelineEvents.set(events);
  }

  renderTimeline(): void {
    if (!this.canvasCtx) return;

    const ctx = this.canvasCtx;
    const canvas = this.timelineCanvas.nativeElement;
    const w = canvas.width;
    const h = canvas.height;

    // Clear
    ctx.clearRect(0, 0, w, h);

    // Background
    ctx.fillStyle = '#111827';
    ctx.fillRect(0, 0, w, h);

    const events = this.timelineEvents();
    if (events.length === 0) return;

    const maxCount = Math.max(...events.map((e) => e.count), 1);
    const barWidth = w / events.length;
    const padding = 8;

    // Draw histogram bars
    events.forEach((event, i) => {
      const barHeight = ((event.count / maxCount) * (h - padding * 2));
      const x = i * barWidth;
      const y = h - padding - barHeight;

      // Bar color based on density
      const intensity = event.count / maxCount;
      if (intensity > 0.8) {
        ctx.fillStyle = 'rgba(239, 68, 68, 0.6)';
      } else if (intensity > 0.5) {
        ctx.fillStyle = 'rgba(245, 158, 11, 0.5)';
      } else if (intensity > 0.2) {
        ctx.fillStyle = 'rgba(59, 130, 246, 0.4)';
      } else {
        ctx.fillStyle = 'rgba(59, 130, 246, 0.2)';
      }

      ctx.fillRect(x + 0.5, y, barWidth - 1, barHeight);
    });

    // Draw brush selection
    if (this.brushStart() && this.brushEnd()) {
      const sx = this.timeToX(this.brushStart()!);
      const ex = this.timeToX(this.brushEnd()!);
      ctx.fillStyle = 'rgba(59, 130, 246, 0.15)';
      ctx.fillRect(Math.min(sx, ex), 0, Math.abs(ex - sx), h);
      ctx.strokeStyle = 'rgba(59, 130, 246, 0.6)';
      ctx.lineWidth = 1;
      ctx.strokeRect(Math.min(sx, ex), 0, Math.abs(ex - sx), h);
    }

    // Draw current time indicator
    const currentX = this.timeToX(this.currentTime());
    if (currentX >= 0 && currentX <= w) {
      ctx.strokeStyle = '#ef4444';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(currentX, 0);
      ctx.lineTo(currentX, h);
      ctx.stroke();
    }

    // Time labels
    ctx.fillStyle = '#5a6a80';
    ctx.font = '10px JetBrains Mono, monospace';
    ctx.textAlign = 'center';

    const labelCount = 6;
    for (let i = 0; i <= labelCount; i++) {
      const x = (w / labelCount) * i;
      const time = this.xToTime(x);
      const label = time.toISOString().substring(11, 16); // HH:mm
      ctx.fillText(label, x, h - 1);
    }
  }

  play(): void {
    if (this.playing()) return;

    this.playing.set(true);
    const stepMs = 60000 * this.speed(); // 1 minute * speed

    this.playbackSubscription = interval(100).subscribe(() => {
      const next = new Date(this.currentTime().getTime() + stepMs);
      if (next > this.endTime()) {
        this.currentTime.set(this.startTime());
      } else {
        this.currentTime.set(next);
      }
      this.renderTimeline();
    });
  }

  pause(): void {
    this.playing.set(false);
    if (this.playbackSubscription) {
      this.playbackSubscription.unsubscribe();
      this.playbackSubscription = null;
    }
  }

  togglePlayback(): void {
    if (this.playing()) {
      this.pause();
    } else {
      this.play();
    }
  }

  setSpeed(speed: number): void {
    this.speed.set(speed);
    if (this.playing()) {
      this.pause();
      this.play();
    }
  }

  skipBackward(): void {
    const step = 5 * 60000 * this.speed();
    const next = new Date(this.currentTime().getTime() - step);
    this.currentTime.set(next < this.startTime() ? this.startTime() : next);
    this.renderTimeline();
  }

  skipForward(): void {
    const step = 5 * 60000 * this.speed();
    const next = new Date(this.currentTime().getTime() + step);
    this.currentTime.set(next > this.endTime() ? this.endTime() : next);
    this.renderTimeline();
  }

  resetTime(): void {
    this.pause();
    this.currentTime.set(new Date());
    this.brushStart.set(null);
    this.brushEnd.set(null);
    this.renderTimeline();
  }

  formatTime(date: Date): string {
    return date.toISOString().replace('T', ' ').substring(0, 19) + 'Z';
  }

  private timeToX(time: Date): number {
    const canvas = this.timelineCanvas.nativeElement;
    const start = this.startTime().getTime();
    const end = this.endTime().getTime();
    const ratio = (time.getTime() - start) / (end - start);
    return ratio * canvas.width;
  }

  private xToTime(x: number): Date {
    const canvas = this.timelineCanvas.nativeElement;
    const start = this.startTime().getTime();
    const end = this.endTime().getTime();
    const ratio = x / canvas.width;
    return new Date(start + ratio * (end - start));
  }
}
