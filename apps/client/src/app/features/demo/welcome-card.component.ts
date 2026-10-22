import { ChangeDetectionStrategy, Component, ElementRef, HostListener, Injector, afterNextRender, computed, effect, inject, viewChild } from '@angular/core';
import { AuthService } from '../../core/services/auth.service';
import { DemoBounds, DemoService } from '../../core/services/demo.service';
import { EmplacementService } from '../../core/services/emplacement.service';
import { DEMO_CARD_STYLES, PORTFOLIO_URL } from './demo-card.styles';
import { sensorBounds, visitorBounds } from './demo-bounds';
import { guestSignal } from './guest-signal';

/**
 * The first thing a demo visitor reads: what they are looking at and where to
 * start. Shown to a guest until dismissed (kept in memory, so it stays closed
 * for the session), and it tells the map once it is on screen so the browser's
 * location prompt comes after it rather than before.
 */
@Component({
  selector: 'app-welcome-card',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (visible()) {
      <div class="scrim">
        <div #dialog class="card" role="dialog" aria-modal="true" aria-labelledby="welcome-title" aria-describedby="welcome-points" (keydown)="onKeydown($event)">
          <div class="brand">
            <img src="assets/brand/sentinel-icon-64.png" alt="" aria-hidden="true" width="24" height="24" />
          </div>
          <h2 id="welcome-title">Welcome to Sentinel</h2>
          <ul id="welcome-points" class="points">
            <li>You're exploring as a guest, so this is read-only. Look around as much as you like; nothing you do can change anything.</li>
            <li>Aircraft, vessels and satellites are live, moving where they are right now.</li>
            <li>The radar, camera and ESM sensor activity is simulated, to show how the platform works.</li>
            <li>
              The <span class="swatch others" aria-hidden="true"></span>violet dots are other people exploring right now,
              and the <span class="swatch you" aria-hidden="true"></span>bright one is you.
            </li>
          </ul>
          <div class="actions">
            <button type="button" class="btn btn-secondary" [disabled]="!sensors()" (click)="focus(sensors())">Fly to the sensors</button>
            <button type="button" class="btn btn-secondary" [disabled]="!visitors()" (click)="focus(visitors())">Show everyone exploring</button>
            <button #explore type="button" class="btn btn-primary" (click)="dismiss()">Explore</button>
          </div>
          <a class="back" [href]="portfolioUrl">Back to msbolton.dev</a>
        </div>
      </div>
    }
  `,
  styles: [
    DEMO_CARD_STYLES,
    `
      .points {
        margin: 0 0 20px;
        padding: 0;
        list-style: none;
        display: flex;
        flex-direction: column;
        gap: 10px;
        font-size: 0.9rem;
        line-height: 1.5;
      }

      .points li {
        position: relative;
        padding-left: 16px;
      }

      .points li::before {
        content: '';
        position: absolute;
        left: 0;
        top: 0.6em;
        width: 5px;
        height: 5px;
        border-radius: 50%;
        background: var(--text-muted);
      }

      .swatch {
        display: inline-block;
        width: 9px;
        height: 9px;
        margin: 0 5px 0 1px;
        border-radius: 50%;
        vertical-align: baseline;
      }

      /* The same violets the map draws visitors in. */
      .swatch.others {
        background: #8b5cf6;
      }

      .swatch.you {
        background: #c4b5fd;
        box-shadow: 0 0 0 2px rgba(167, 139, 250, 0.55);
      }
    `,
  ],
})
export class WelcomeCardComponent {
  protected readonly demo = inject(DemoService);
  private readonly emplacements = inject(EmplacementService);
  private readonly injector = inject(Injector);
  private readonly guest = guestSignal(inject(AuthService));
  private readonly dialog = viewChild<ElementRef<HTMLElement>>('dialog');
  private readonly explore = viewChild<ElementRef<HTMLButtonElement>>('explore');
  protected readonly portfolioUrl = PORTFOLIO_URL;

  protected readonly visible = computed(() => this.guest() && !this.demo.sessionEnded() && !this.demo.welcomeDismissed());

  private readonly visitorsFeedId = computed(() => this.demo.session()?.visitorsFeedId);
  protected readonly sensors = computed(() => sensorBounds(this.emplacements.emplacements(), this.visitorsFeedId()));
  protected readonly visitors = computed(() => visitorBounds(this.emplacements.emplacements(), this.visitorsFeedId()));

  constructor() {
    effect(() => {
      if (this.visible()) {
        // Once the card is drawn: put focus on the way in, and let the map ask for the location.
        afterNextRender(
          () => {
            this.explore()?.nativeElement.focus();
            this.demo.welcomeShown.set(true);
          },
          { injector: this.injector },
        );
      }
    });
  }

  protected focus(bounds: DemoBounds | null): void {
    if (bounds) {
      this.demo.focusBounds.set(bounds);
      this.dismiss();
    }
  }

  protected dismiss(): void {
    this.demo.welcomeShown.set(true);
    this.demo.welcomeDismissed.set(true);
  }

  @HostListener('document:keydown.escape')
  protected onEscape(): void {
    if (this.visible()) this.dismiss();
  }

  /** Keeps Tab inside the card while it is open. */
  protected onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Tab') return;
    const root = this.dialog()?.nativeElement;
    if (!root) return;
    const items = Array.from(root.querySelectorAll<HTMLElement>('button:not([disabled]), a[href]'));
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }
}
