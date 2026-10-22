import { ChangeDetectionStrategy, Component, ElementRef, Injector, afterNextRender, effect, inject, viewChild } from '@angular/core';
import { Router } from '@angular/router';
import { DemoService } from '../../core/services/demo.service';
import { trapTab } from './focus-trap';
import { DEMO_CARD_STYLES, PORTFOLIO_URL } from './demo-card.styles';

/** Shown over the app when the guest's session has ended; its button starts a fresh one. */
@Component({
  selector: 'app-session-ended',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (demo.sessionEnded()) {
      <div class="scrim">
        <div #dialog class="card" role="dialog" aria-modal="true" aria-labelledby="session-ended-title" aria-describedby="session-ended-body" (keydown)="onKeydown($event)">
          <h2 id="session-ended-title">Your demo session has ended</h2>
          <p id="session-ended-body">Demo sessions are short, so everyone gets a turn. Start a new session to keep exploring.</p>
          <div class="actions">
            <button #restart type="button" class="btn btn-primary" (click)="restartSession()">Start a new session</button>
          </div>
          <a class="back" [href]="portfolioUrl">Back to msbolton.dev</a>
        </div>
      </div>
    }
  `,
  styles: [DEMO_CARD_STYLES],
})
export class SessionEndedComponent {
  protected readonly demo = inject(DemoService);
  private readonly router = inject(Router);
  private readonly injector = inject(Injector);
  private readonly dialog = viewChild<ElementRef<HTMLElement>>('dialog');
  private readonly restart = viewChild<ElementRef<HTMLButtonElement>>('restart');
  protected readonly portfolioUrl = PORTFOLIO_URL;

  constructor() {
    // Put focus on the one thing to do as the card appears.
    effect(() => {
      if (this.demo.sessionEnded()) {
        afterNextRender(() => this.restart()?.nativeElement.focus(), { injector: this.injector });
      }
    });
  }

  /** Keeps Tab inside the card while it is open. */
  protected onKeydown(event: KeyboardEvent): void {
    trapTab(event, this.dialog()?.nativeElement);
  }

  protected restartSession(): void {
    // DemoStartComponent sees the ended session and starts a new guest.
    void this.router.navigateByUrl('/demo');
  }
}
