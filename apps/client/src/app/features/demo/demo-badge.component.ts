import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { AuthService } from '../../core/services/auth.service';
import { guestSignal } from './guest-signal';

/** A small "Demo" pill for the shell, shown only to a demo guest. */
@Component({
  selector: 'app-demo-badge',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (guest()) {
      <span class="badge-demo" title="A read-only demo of Sentinel. Nothing here can be changed.">
        <span class="dot" aria-hidden="true"></span>
        <span>Demo</span><span class="sr-only"> mode: read-only</span>
      </span>
    }
  `,
  styles: [
    `
      :host {
        display: block;
      }

      .badge-demo {
        display: inline-flex;
        align-items: center;
        gap: 6px;
        padding: 3px 10px;
        border-radius: 999px;
        border: 1px solid color-mix(in srgb, var(--accent-purple) 55%, transparent);
        background: color-mix(in srgb, var(--accent-purple) 18%, transparent);
        color: var(--text-primary);
        font-family: var(--font-mono);
        font-size: 0.68rem;
        font-weight: 600;
        letter-spacing: 0.1em;
        text-transform: uppercase;
        line-height: 1.4;
        white-space: nowrap;
      }

      .dot {
        width: 6px;
        height: 6px;
        border-radius: 50%;
        background: var(--accent-purple);
      }

      .sr-only {
        position: absolute;
        width: 1px;
        height: 1px;
        overflow: hidden;
        clip: rect(0 0 0 0);
        white-space: nowrap;
      }
    `,
  ],
})
export class DemoBadgeComponent {
  protected readonly guest = guestSignal(inject(AuthService));
}
