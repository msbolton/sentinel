import { Component, OnInit, inject, signal } from '@angular/core';
import { Router } from '@angular/router';
import { AuthService } from '../../core/services/auth.service';
import { DemoService } from '../../core/services/demo.service';

const FULL_MESSAGE = 'The demo is full right now — try again in a few minutes.';

/**
 * Entry point of the public demo (`#/demo`): signs the visitor in as a guest
 * and sends them to the map. A visitor who is already signed in keeps that
 * session (a real user is never replaced by a guest).
 */
@Component({
  selector: 'app-demo-start',
  standalone: true,
  template: `
    <main class="demo-start">
      <img src="assets/brand/sentinel-icon-256.png" alt="SENTINEL" width="72" height="72" />
      <h1>SENTINEL</h1>
      @if (message(); as text) {
        <p class="message" role="alert">{{ text }}</p>
        <button type="button" class="retry" (click)="start()">Try again</button>
      } @else {
        <p class="message" role="status">Starting the demo…</p>
      }
    </main>
  `,
  styles: [
    `
      .demo-start {
        min-height: 100vh;
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        gap: 12px;
        padding: 16px;
        text-align: center;
        color: var(--text-primary, #e6edf3);
        background: var(--bg-primary, #0b1220);
      }
      h1 {
        margin: 0;
        letter-spacing: 0.2em;
      }
      .message {
        margin: 0;
        max-width: 28rem;
      }
      .retry {
        padding: 8px 16px;
        cursor: pointer;
      }
    `,
  ],
})
export class DemoStartComponent implements OnInit {
  private readonly auth = inject(AuthService);
  private readonly demo = inject(DemoService);
  private readonly router = inject(Router);

  /** Why the visitor is still here; null while starting. */
  protected readonly message = signal<string | null>(null);

  ngOnInit(): void {
    // A guest whose session ended still holds dead tokens, so it starts over.
    const keepSession = this.auth.isAuthenticated() && !(this.auth.isGuest() && this.demo.sessionEnded());
    if (keepSession) {
      void this.toMap();
      return;
    }
    void this.start();
  }

  protected async start(): Promise<void> {
    this.message.set(null);
    const result = await this.demo.startSession();
    if (result === 'ok') {
      await this.toMap();
    } else if (result === 'full') {
      this.message.set(FULL_MESSAGE);
    } else {
      this.message.set(this.demo.errorMessage() ?? 'Something went wrong starting the demo — try again in a moment.');
    }
  }

  private toMap(): Promise<boolean> {
    // replaceUrl: Back from the map must not land on /demo and start another guest.
    return this.router.navigateByUrl('/map', { replaceUrl: true });
  }
}
