import { ChangeDetectionStrategy, Component, OnInit, computed, inject, input, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { HttpErrorResponse } from '@angular/common/http';
import { IngestTokenService } from '../../core/services/ingest-token.service';
import { AuthService } from '../../core/services/auth.service';
import { Feed } from '../../shared/models/feed.model';
import { IngestToken } from '../../shared/models/ingest-token.model';
import { levelsUpTo, normalizeClearance, rank } from '../../shared/models/classification';

/**
 * A gRPC connection's ingest tokens: list, issue (the secret is shown once,
 * never fetched again) and revoke (a second click confirms).
 */
@Component({
  selector: 'app-ingest-tokens',
  standalone: true,
  imports: [DatePipe],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './ingest-tokens.component.html',
  styleUrl: './ingest-tokens.component.scss',
})
export class IngestTokensComponent implements OnInit {
  readonly feed = input.required<Feed>();

  private readonly service = inject(IngestTokenService);
  private readonly auth = inject(AuthService);

  protected readonly tokens = signal<IngestToken[]>([]);
  protected readonly secret = signal<string | null>(null);
  protected readonly confirming = signal<string | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly name = signal('');
  protected readonly level = signal('');
  protected readonly expiresAt = signal('');
  /** A create is out: a second one would mint a token whose secret overwrites the first's. */
  protected readonly issuing = signal(false);
  protected readonly copyState = signal<'copied' | 'failed' | null>(null);

  /** The connection's level up to the user's clearance: a lower ceiling would refuse every report. */
  protected readonly levels = computed(() => {
    const floor = rank(normalizeClearance(String(this.feed().config?.['classification'] ?? 'UNCLASSIFIED')));
    return levelsUpTo(this.auth.getClassificationLevel()).filter((l) => rank(l) >= floor);
  });

  ngOnInit(): void {
    this.level.set(this.levels()[0] ?? 'UNCLASSIFIED');
    this.reload();
  }

  /** `keepError` leaves a message already on screen (a failed revoke) in place when the list arrives. */
  protected reload(keepError = false): void {
    this.service.list(this.feed().id).subscribe({
      next: (list) => {
        this.tokens.set(list ?? []);
        if (!keepError) this.error.set(null);
      },
      error: (err) => this.error.set(messageOf(err)),
    });
  }

  protected issue(event: Event): void {
    event.preventDefault();
    // One secret at a time: the operator copies it and clicks Done before issuing the next token.
    if (this.issuing() || this.secret() !== null) return;
    this.error.set(null);
    this.issuing.set(true);
    const body: { name: string; classification: string; expiresAt?: string } = { name: this.name().trim(), classification: this.level() };
    if (this.expiresAt()) body.expiresAt = new Date(this.expiresAt()).toISOString();
    this.service.create(this.feed().id, body).subscribe({
      next: (created) => {
        this.issuing.set(false);
        this.copyState.set(null);
        this.secret.set(created.token);
        this.name.set('');
        this.reload();
      },
      error: (err) => {
        this.issuing.set(false);
        this.error.set(messageOf(err));
      },
    });
  }

  protected dismiss(): void {
    this.secret.set(null);
    this.copyState.set(null);
  }

  protected expired(t: IngestToken): boolean {
    return !t.revokedAt && !!t.expiresAt && new Date(t.expiresAt).getTime() <= Date.now();
  }

  protected revoke(t: IngestToken): void {
    this.error.set(null);
    if (this.confirming() !== t.id) {
      this.confirming.set(t.id);
      return;
    }
    this.confirming.set(null);
    this.service.revoke(this.feed().id, t.id).subscribe({
      next: () => this.reload(),
      error: (err) => {
        this.error.set(messageOf(err));
        // Another operator may have removed it already: show what is there now, and keep the message.
        this.reload(true);
      },
    });
  }

  protected async copy(): Promise<void> {
    const s = this.secret();
    if (!s) return;
    try {
      await navigator.clipboard.writeText(s);
      this.copyState.set('copied');
    } catch {
      // no clipboard (insecure origin): the secret stays on screen to copy by hand
      this.copyState.set('failed');
    }
  }

  protected text(event: Event): string {
    return (event.target as HTMLInputElement | HTMLSelectElement).value;
  }
}

function messageOf(err: unknown): string {
  if (err instanceof HttpErrorResponse) {
    if (Array.isArray(err.error?.details) && err.error.details.length > 0) {
      return err.error.details.map((p: { path: string; message: string }) => `${p.path.replace(/^\//, '')} ${p.message}`).join('; ');
    }
    if (typeof err.error?.error === 'string') return err.error.error;
  }
  return 'The token request failed.';
}
