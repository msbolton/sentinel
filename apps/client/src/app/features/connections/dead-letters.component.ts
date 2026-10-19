import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  ElementRef,
  LOCALE_ID,
  computed,
  inject,
  input,
  signal,
  viewChild,
} from '@angular/core';
import { DOCUMENT, DatePipe, DecimalPipe, formatDate, formatNumber } from '@angular/common';
import { HttpErrorResponse } from '@angular/common/http';
import { Subscription } from 'rxjs';
import { FeedService } from '../../core/services/feed.service';
import { DEAD_LETTERS_LIMIT, DeadLetter, DeadLetterPage } from '../../shared/models/feed.model';
import { senderTime as recordedTime } from './send-guide';

/**
 * Why there is nothing to list: the user is not cleared (403), the connection is gone (404), the server cannot read
 * them (501), or it is worth another try.
 */
type Problem = 'forbidden' | 'gone' | 'disabled' | 'failed';

/** What a request was told, and what it was asked: the payloads belong to the page only if they were asked for. */
interface Answer {
  page: DeadLetterPage;
  withPayload: boolean;
}

/** How a reject's time is written: in the table, and in the name of its payload. */
const TIME_FORMAT = 'MMM d HH:mm:ss';

function problemOf(err: unknown): Problem {
  const status = err instanceof HttpErrorResponse ? err.status : 0;
  return status === 403 ? 'forbidden' : status === 404 ? 'gone' : status === 501 ? 'disabled' : 'failed';
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
  private readonly doc = inject(DOCUMENT);

  readonly feedId = input.required<string>();
  /** A time the server may have left out, or written as Go's zero time: null for those. */
  readonly time = recordedTime;
  readonly timeFormat = TIME_FORMAT;
  /** How many rejects it asks for. A list as long as that may be only the newest of more. */
  readonly limit = DEAD_LETTERS_LIMIT;

  /** The latest answer; null before one, and after a failure. */
  private readonly answer = signal<Answer | null>(null);
  readonly page = computed(() => this.answer()?.page ?? null);
  readonly loading = signal(false);
  /** What stopped the latest request; null while nothing has. */
  readonly problem = signal<Problem | null>(null);
  /** Whether the payloads are wanted: Refresh and Retry ask for them while they are, unless asking for them is what failed. */
  readonly payloads = signal(false);
  /** Whether to show them: wanted, and in the page (a page asked for without them has none to show). */
  readonly showPayloads = computed(() => this.payloads() && this.answer()?.withPayload === true);
  /** Starts every id in this panel, so two open panels never share one. */
  readonly idBase = computed(() => `dl-${this.feedId()}`);
  /** Whether there is anything to press: not when the user may not read the rejects, the server cannot, or the connection is gone. */
  readonly actionable = computed(() => this.problem() === null || this.problem() === 'failed');
  /** Show and Hide payload need a list with a reject in it to act on. */
  readonly payloadToggle = computed(() => this.actionable() && (this.page()?.letters.length ?? 0) > 0);

  // the controls the focus can be on; their references are named so as not to hide the members the template calls
  private readonly summary = viewChild<ElementRef<HTMLElement>>('summaryRef');
  private readonly reloadButton = viewChild<ElementRef<HTMLElement>>('reloadRef');
  private readonly payloadButton = viewChild<ElementRef<HTMLElement>>('payloadRef');

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

  /** The id of a reject's payload caption, which says how its block is encoded or cut. */
  captionId(letter: DeadLetter): string {
    return `${this.idBase()}-${letter.partition}-${letter.offset}`;
  }

  /** What names a reject's payload block: the reject it belongs to, as its row shows it ("at Oct 18 12:00:00 (parse_error)"). */
  payloadNameOf(letter: DeadLetter): string {
    const at = this.time(letter.at);
    const when = at === null ? '' : ` at ${formatDate(at, TIME_FORMAT, this.locale)}`;
    return `Payload of the reject${when} (${letter.reason})`;
  }

  private load(): void {
    // One question at a time, whoever asks: an older answer must never replace a newer one.
    this.request?.unsubscribe();
    this.asked = true;
    this.loading.set(true);
    this.problem.set(null);
    const withPayload = this.payloads();
    this.request = this.feeds.deadLetters(this.feedId(), { payload: withPayload }).subscribe({
      next: (page) => {
        this.answer.set({ page, withPayload });
        this.loading.set(false);
        this.keepFocus();
      },
      error: (err: unknown) => {
        // a list that can no longer be vouched for is not left on show
        this.answer.set(null);
        this.problem.set(problemOf(err));
        this.loading.set(false);
        // and with it goes the button that says whether the payloads are on, so Retry asks for the plain list
        if (withPayload) {
          this.payloads.set(false);
        }
        this.keepFocus();
      },
    });
  }

  /**
   * An answer can take away the button the focus is on: the list it acts on is gone, or has nothing in it. The page
   * would get the focus, so it moves to a button that stays (Retry, Refresh), or the summary when none does.
   */
  private keepFocus(): void {
    const active = this.doc.activeElement;
    const onPayload = active !== null && active === this.payloadButton()?.nativeElement;
    const onReload = active !== null && active === this.reloadButton()?.nativeElement;
    if ((onPayload && !this.payloadToggle()) || (onReload && !this.actionable())) {
      (this.actionable() ? this.reloadButton() : this.summary())?.nativeElement.focus();
    }
  }
}
