import { ChangeDetectionStrategy, Component, DestroyRef, LOCALE_ID, computed, inject, input, signal } from '@angular/core';
import { DatePipe, DecimalPipe, formatNumber } from '@angular/common';
import { HttpErrorResponse } from '@angular/common/http';
import { Subscription } from 'rxjs';
import { FeedService } from '../../core/services/feed.service';
import { DeadLetter, DeadLetterPage } from '../../shared/models/feed.model';
import { senderTime } from './send-guide';

/** Why there is nothing to list: the user is not cleared (403), the server cannot read them (501), or it is worth another try. */
type Problem = 'forbidden' | 'disabled' | 'failed';

/** What a request was told, and what it was asked: the payloads belong to the page only if they were asked for. */
interface Answer {
  page: DeadLetterPage;
  withPayload: boolean;
}

function problemOf(err: unknown): Problem {
  const status = err instanceof HttpErrorResponse ? err.status : 0;
  return status === 403 ? 'forbidden' : status === 501 ? 'disabled' : 'failed';
}

/**
 * The messages a connection rejected, newest first: when, why, who sent them and what the decoder said. It is
 * closed until opened and asks the server the first time it is, then only when told to (Refresh or Retry). The
 * payloads are a second question (the server cuts each to 1 KiB), asked only for when they are wanted.
 * One panel serves one connection: the page makes one per row, so `feedId` does not change.
 */
@Component({
  selector: 'app-dead-letters',
  standalone: true,
  imports: [DatePipe, DecimalPipe],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './dead-letters.component.html',
  styleUrl: './dead-letters.component.scss',
})
export class DeadLettersComponent {
  private readonly feeds = inject(FeedService);
  private readonly locale = inject(LOCALE_ID);

  readonly feedId = input.required<string>();
  /** A time the server may have left out, or written as Go's zero time: null for those. */
  readonly time = senderTime;

  /** The latest answer; null before one, and after a failure. */
  private readonly answer = signal<Answer | null>(null);
  readonly page = computed(() => this.answer()?.page ?? null);
  readonly loading = signal(false);
  /** What stopped the latest request; null while nothing has. */
  readonly problem = signal<Problem | null>(null);
  /** Whether the payloads are wanted: Refresh and Retry ask for them while they are. */
  readonly payloads = signal(false);
  /** Whether to show them: wanted, and in the page (a page asked for without them has none to show). */
  readonly showPayloads = computed(() => this.payloads() && this.answer()?.withPayload === true);
  /** Starts every id in this panel, so two open panels never share one. */
  readonly idBase = computed(() => `dl-${this.feedId()}`);

  private asked = false;
  private request: Subscription | null = null;

  constructor() {
    inject(DestroyRef).onDestroy(() => this.request?.unsubscribe());
  }

  /** The panel was opened or closed: opening it for the first time asks, and later openings show what is there. */
  onToggle(event: Event): void {
    if ((event.target as HTMLDetailsElement).open && !this.asked) {
      this.load();
    }
  }

  /** Refresh, and Retry after a failure. A press while a request is out changes nothing. */
  reload(): void {
    if (!this.loading()) {
      this.load();
    }
  }

  /** Show payload asks again with the payloads; Hide payload only stops showing them. */
  togglePayloads(): void {
    if (this.loading()) {
      return;
    }
    const show = !this.payloads();
    this.payloads.set(show);
    if (show) {
      this.load();
    }
  }

  /** What the payload block says it is: "Payload · binary (base64) · cut to 1 KiB of 4,096 bytes". */
  captionOf(letter: DeadLetter): string {
    const parts = ['Payload'];
    if (letter.payloadEncoding === 'base64') {
      parts.push('binary (base64)');
    }
    if (letter.payloadTruncated) {
      parts.push(`cut to 1 KiB of ${formatNumber(letter.size, this.locale)} bytes`);
    }
    return parts.join(' · ');
  }

  /** The id of a reject's payload caption, which names its block. */
  captionId(letter: DeadLetter): string {
    return `${this.idBase()}-${letter.partition}-${letter.offset}`;
  }

  private load(): void {
    this.asked = true;
    this.loading.set(true);
    this.problem.set(null);
    const withPayload = this.payloads();
    this.request = this.feeds.deadLetters(this.feedId(), { payload: withPayload }).subscribe({
      next: (page) => {
        this.answer.set({ page, withPayload });
        this.loading.set(false);
      },
      error: (err: unknown) => {
        // a list that can no longer be vouched for is not left on show
        this.answer.set(null);
        this.problem.set(problemOf(err));
        this.loading.set(false);
      },
    });
  }
}
