import {
  Component,
  ChangeDetectionStrategy,
  OnDestroy,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Subscription } from 'rxjs';
import { TrackPanelStore } from './track-panel.store';
import { TrackRenderService } from './track-render.service';
import { TrackApiService } from '../../core/services/track-api.service';

@Component({
  selector: 'app-track-panel',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule],
  templateUrl: './track-panel.component.html',
  styleUrls: ['./track-panel.component.scss'],
  // NOTE: TrackPanelStore and TrackRenderService are provided by MapComponent (parent).
  // This component inherits them through the DI tree.
})
export class TrackPanelComponent implements OnDestroy {
  readonly speeds = [0.5, 1, 2, 5, 10];
  private replaySub: Subscription | null = null;

  constructor(
    readonly store: TrackPanelStore,
    private readonly renderService: TrackRenderService,
    private readonly trackApi: TrackApiService,
  ) {}

  ngOnDestroy(): void {
    this.stopReplay();
  }

  play(): void {
    const points = this.store.allPoints();
    const timeRange = this.store.timeRange();
    if (!timeRange || points.length === 0) return;

    const entityId = this.store.selectedEntityId()!;
    const currentPoint = this.store.currentPoint();

    const startTime = currentPoint
      ? new Date(new Date(currentPoint.timestamp).getTime() + 1).toISOString()
      : timeRange.start;

    this.renderService.setupReplayPolylines(points);
    this.store.isPlaying.set(true);

    let replayIdx = this.store.currentIndex() >= 0 ? this.store.currentIndex() : 0;

    this.replaySub = this.trackApi.replayStream(entityId, {
      startTime,
      endTime: timeRange.end,
      speedMultiplier: this.store.speedMultiplier(),
    }).subscribe({
      next: () => {
        replayIdx++;
        if (replayIdx < points.length) {
          this.store.currentIndex.set(replayIdx);
          this.renderService.updateReplayIndex(replayIdx);
        }
      },
      complete: () => {
        this.store.isPlaying.set(false);
      },
      error: () => {
        this.store.isPlaying.set(false);
      },
    });
  }

  pause(): void {
    this.stopReplay();
    this.store.isPlaying.set(false);
  }

  togglePlay(): void {
    if (this.store.isPlaying()) {
      this.pause();
    } else {
      this.play();
    }
  }

  onSpeedChange(speed: number): void {
    this.store.speedMultiplier.set(speed);
    if (this.store.isPlaying()) {
      this.stopReplay();
      this.play();
    }
  }

  onScrub(event: Event): void {
    const input = event.target as HTMLInputElement;
    const idx = parseInt(input.value, 10);
    this.stopReplay();
    this.store.isPlaying.set(false);
    this.store.currentIndex.set(idx);
    this.renderService.updateReplayIndex(idx);
  }

  close(): void {
    this.stopReplay();
    this.renderService.clearAll();
    this.store.close();
  }

  private stopReplay(): void {
    if (this.replaySub) {
      this.replaySub.unsubscribe();
      this.replaySub = null;
    }
  }
}
