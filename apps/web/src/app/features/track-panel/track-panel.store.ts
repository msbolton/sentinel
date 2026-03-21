import { Injectable, signal, computed } from '@angular/core';
import { TrackPoint } from '../../core/services/track-api.service';

@Injectable()
export class TrackPanelStore {
  readonly selectedEntityId = signal<string | null>(null);
  readonly selectedEntityName = signal<string>('');
  readonly allPoints = signal<TrackPoint[]>([]);
  readonly currentIndex = signal<number>(-1);
  readonly isPlaying = signal<boolean>(false);
  readonly speedMultiplier = signal<number>(1);
  readonly isExpanded = signal<boolean>(true);

  readonly isOpen = computed(() => this.selectedEntityId() !== null);

  readonly currentPoint = computed(() => {
    const idx = this.currentIndex();
    const points = this.allPoints();
    return idx >= 0 && idx < points.length ? points[idx] : null;
  });

  readonly timeRange = computed(() => {
    const points = this.allPoints();
    if (points.length === 0) return null;
    return {
      start: points[0].timestamp,
      end: points[points.length - 1].timestamp,
    };
  });

  readonly progress = computed(() => {
    const points = this.allPoints();
    const idx = this.currentIndex();
    if (points.length === 0) return 0;
    return idx / (points.length - 1);
  });

  open(entityId: string, entityName: string, points: TrackPoint[]): void {
    this.selectedEntityId.set(entityId);
    this.selectedEntityName.set(entityName);
    this.allPoints.set(points);
    this.currentIndex.set(-1);
    this.isPlaying.set(false);
    this.speedMultiplier.set(1);
    this.isExpanded.set(true);
  }

  close(): void {
    this.selectedEntityId.set(null);
    this.selectedEntityName.set('');
    this.allPoints.set([]);
    this.currentIndex.set(-1);
    this.isPlaying.set(false);
  }

  toggleExpanded(): void {
    this.isExpanded.update((v) => !v);
  }
}
