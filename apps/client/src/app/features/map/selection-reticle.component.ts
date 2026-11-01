import { ChangeDetectionStrategy, Component, DestroyRef, ElementRef, computed, inject, signal } from '@angular/core';
import { SelectionService } from '../../core/services/selection.service';

/** Half the side of the box the brackets frame, and how far the reticle lines stop short of the track. */
const HALF = 20;
/** Length of each bracket arm. */
const ARM = 8;
/** Gap between the end of each reticle line and the track. */
const GAP = 40;

/** Snaps to the middle of a pixel so 1px strokes stay crisp. */
const px = (v: number) => Math.floor(v) + 0.5;

/**
 * White corner brackets and faint cyan reticle lines around the selected
 * track's screen position. Draws nothing when there is no position.
 */
@Component({
  selector: 'app-selection-reticle',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (shape(); as s) {
      <svg [attr.width]="w()" [attr.height]="h()" [attr.viewBox]="'0 0 ' + w() + ' ' + h()" aria-hidden="true">
        @for (l of s.lines; track $index) {
          <line class="reticle" [attr.x1]="l.x1" [attr.y1]="l.y1" [attr.x2]="l.x2" [attr.y2]="l.y2" />
        }
        @for (d of s.brackets; track $index) {
          <path class="bracket" [attr.d]="d" />
        }
      </svg>
    }
  `,
  styles: [`
    :host {
      position: absolute;
      inset: 0;
      pointer-events: none;
      z-index: 50;
    }
    svg { display: block; overflow: visible; }
    .bracket { stroke: #fff; stroke-width: 1.2; fill: none; }
    .reticle { stroke: var(--accent-cyan); stroke-width: 1; opacity: 0.4; }
  `],
})
export class SelectionReticleComponent {
  private readonly selection = inject(SelectionService);
  private readonly host = inject<ElementRef<HTMLElement>>(ElementRef).nativeElement;

  protected readonly point = this.selection.screenPoint;
  protected readonly w = signal(typeof window !== 'undefined' ? window.innerWidth : 0);
  protected readonly h = signal(typeof window !== 'undefined' ? window.innerHeight : 0);

  protected readonly shape = computed(() => {
    const p = this.point();
    if (!p) return null;
    const x = px(p.x);
    const y = px(p.y);
    const l = px(p.x - HALF), r = px(p.x + HALF), t = px(p.y - HALF), b = px(p.y + HALF);
    return {
      brackets: [
        `M ${l} ${t + ARM} V ${t} H ${l + ARM}`,
        `M ${r - ARM} ${t} H ${r} V ${t + ARM}`,
        `M ${r} ${b - ARM} V ${b} H ${r - ARM}`,
        `M ${l + ARM} ${b} H ${l} V ${b - ARM}`,
      ],
      lines: [
        { x1: 0, y1: y, x2: px(p.x - GAP), y2: y },
        { x1: this.w(), y1: y, x2: px(p.x + GAP), y2: y },
        { x1: x, y1: 0, x2: x, y2: px(p.y - GAP) },
        { x1: x, y1: this.h(), x2: x, y2: px(p.y + GAP) },
      ],
    };
  });

  constructor() {
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => {
      this.w.set(this.host.clientWidth);
      this.h.set(this.host.clientHeight);
    });
    observer.observe(this.host);
    inject(DestroyRef).onDestroy(() => observer.disconnect());
  }
}
