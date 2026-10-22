import { ChangeDetectionStrategy, Component, inject, input } from '@angular/core';
import { AuthService } from '../../core/services/auth.service';
import { guestSignal } from './guest-signal';

/** A small "Demo" pill for the shell, shown only to a demo guest. */
@Component({
  selector: 'app-demo-badge',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (guest()) {
      <span class="badge-demo" [class.compact]="compact()" title="A read-only demo of Sentinel. Nothing here can be changed.">
        <span class="dot" aria-hidden="true"></span>
        <span [class.sr-only]="compact()">Demo</span><span class="sr-only"> mode: read-only</span>
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

      /*
       * Width budget: the expanded rail's content is 208 - 16 padding - 1 border = 191px and the
       * pill is about 64px (6 dot + 6 gap + 4 x 7.6 text + 20 padding + 2 border). The collapsed
       * rail's content is 56 - 16 - 1 = 39px, so there the pill shrinks to a 24px dot-only circle.
       */
      .badge-demo.compact {
        box-sizing: border-box;
        width: 24px;
        height: 24px;
        padding: 0;
        justify-content: center;
        gap: 0;
      }

      .badge-demo.compact .dot {
        width: 8px;
        height: 8px;
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
  /** True in the collapsed rail: the pill becomes a dot, its text kept for screen readers. */
  readonly compact = input(false);
  protected readonly guest = guestSignal(inject(AuthService));
}
