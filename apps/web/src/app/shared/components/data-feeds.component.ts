import {
  Component,
  ChangeDetectionStrategy,
  signal,
  computed,
  inject,
  HostListener,
  ElementRef,
  OnInit,
} from '@angular/core';
import { DataFeedService, DataFeed } from '../../core/services/data-feed.service';

@Component({
  selector: 'app-data-feeds',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="pill-container" [class.expanded]="expanded()">
      <!-- Pill / Header bar -->
      <button class="pill-header" (click)="toggle()">
        <span class="pill-label">DATA FEEDS</span>
        @if (activeCount() > 0) {
          <span class="feed-badge">{{ activeCount() }}</span>
        }
        <svg class="pill-icon" [class.rotated]="expanded()"
             width="14" height="14" viewBox="0 0 24 24"
             fill="none" stroke="currentColor" stroke-width="2"
             stroke-linecap="round" stroke-linejoin="round">
          <line x1="12" y1="5" x2="12" y2="19"/>
          <line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
      </button>

      <!-- Expanded panel -->
      @if (expanded()) {
        <div class="pill-panel">
          @for (feed of feeds(); track feed.id) {
            <div class="feed-row">
              <div class="feed-info">
                <span class="feed-name">{{ feed.name }}</span>
                <span class="feed-type">{{ feed.sourceType }}</span>
              </div>
              <button
                class="toggle-switch"
                [class.on]="feed.enabled"
                [class.busy]="toggling() === feed.id"
                [disabled]="toggling() === feed.id"
                (click)="toggleFeed(feed)"
                [attr.aria-label]="(feed.enabled ? 'Disable' : 'Enable') + ' ' + feed.name">
                <span class="toggle-thumb"></span>
              </button>
            </div>
          }
          @if (feeds().length === 0) {
            <div class="feed-empty">
              @if (feedService.hasLoaded) {
                No feeds configured
              } @else {
                Ingest service offline
              }
            </div>
          }
        </div>
      }
    </div>
  `,
  styles: [`
    :host {
      display: block;
    }

    .pill-container {
      display: inline-flex;
      flex-direction: column;
    }

    .pill-container.expanded {
      width: 260px;
    }

    .pill-header {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      padding: 8px 14px;
      border-radius: 20px;
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      cursor: pointer;
      font-family: var(--font-mono);
      font-size: 0.7rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--text-secondary);
      white-space: nowrap;
      transition: color var(--transition-fast), border-color var(--transition-fast);

      &:hover {
        color: var(--text-primary);
        border-color: color-mix(in srgb, var(--border-color) 100%, var(--text-muted) 30%);
      }
    }

    .pill-container.expanded .pill-header {
      width: 100%;
      border-radius: var(--radius-lg) var(--radius-lg) 0 0;
    }

    .pill-icon {
      transition: transform 200ms ease;
      flex-shrink: 0;
    }

    .pill-icon.rotated {
      transform: rotate(45deg);
    }

    .feed-badge {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-width: 18px;
      height: 18px;
      padding: 0 5px;
      border-radius: 9px;
      background: var(--accent-green);
      color: #000;
      font-size: 0.65rem;
      font-weight: 700;
      line-height: 1;
    }

    .pill-panel {
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      border-top: none;
      border-radius: 0 0 var(--radius-lg) var(--radius-lg);
      padding: 6px;
      animation: floatIn 200ms cubic-bezier(0.16, 1, 0.3, 1);
    }

    .feed-row {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      padding: 8px 10px;
      border-radius: var(--radius-sm);
      transition: background var(--transition-fast);

      &:hover {
        background: color-mix(in srgb, var(--text-muted) 8%, transparent);
      }
    }

    .feed-info {
      display: flex;
      align-items: center;
      gap: 8px;
      min-width: 0;
    }

    .feed-name {
      font-size: 0.8rem;
      font-weight: 500;
      color: var(--text-primary);
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .feed-type {
      font-family: var(--font-mono);
      font-size: 0.6rem;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      padding: 2px 5px;
      border-radius: 3px;
      background: color-mix(in srgb, var(--text-muted) 12%, transparent);
      color: var(--text-muted);
      white-space: nowrap;
    }

    .feed-empty {
      padding: 12px 10px;
      font-size: 0.75rem;
      color: var(--text-muted);
      text-align: center;
    }

    /* Hardware-style toggle switch */
    .toggle-switch {
      position: relative;
      width: 36px;
      height: 18px;
      border-radius: 9px;
      background: transparent;
      border: 1.5px solid var(--bg-primary);
      cursor: pointer;
      flex-shrink: 0;
      padding: 0;
      transition: border-color 200ms ease;

      &.on {
        border-color: var(--accent-green);
      }

      &.busy {
        opacity: 0.5;
        cursor: wait;
      }

      &:disabled {
        pointer-events: none;
      }
    }

    .toggle-thumb {
      position: absolute;
      top: 2px;
      left: 2px;
      width: 12px;
      height: 12px;
      border-radius: 50%;
      background: var(--text-muted);
      transition: transform 200ms ease, background 200ms ease, box-shadow 200ms ease;

      .toggle-switch.on & {
        transform: translateX(18px);
        background: var(--accent-green);
        box-shadow: 0 0 6px var(--accent-green);
      }
    }

    @keyframes floatIn {
      from {
        opacity: 0;
        transform: translateY(-4px);
      }
      to {
        opacity: 1;
        transform: translateY(0);
      }
    }
  `],
})
export class DataFeedsComponent implements OnInit {
  readonly feedService = inject(DataFeedService);
  private readonly elementRef = inject(ElementRef);

  readonly expanded = signal(false);
  readonly toggling = signal<string | null>(null);
  readonly feeds = this.feedService.feeds;
  readonly activeCount = computed(() =>
    this.feeds().filter((f) => f.enabled).length,
  );

  ngOnInit(): void {
    this.feedService.loadFeeds();
  }

  toggle(): void {
    this.expanded.update((v) => !v);
    if (this.expanded()) {
      this.feedService.loadFeeds();
    }
  }

  toggleFeed(feed: DataFeed): void {
    this.toggling.set(feed.id);
    this.feedService.toggleFeed(feed.id, !feed.enabled).subscribe({
      next: () => this.toggling.set(null),
      error: (err) => {
        console.error('Failed to toggle feed:', err);
        this.toggling.set(null);
      },
    });
  }

  @HostListener('document:click', ['$event'])
  onDocumentClick(event: MouseEvent): void {
    if (!this.elementRef.nativeElement.contains(event.target)) {
      this.expanded.set(false);
    }
  }
}
