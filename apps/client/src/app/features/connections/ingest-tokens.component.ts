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

  /** The connection's level up to the user's clearance: a lower ceiling would refuse every report. */
  protected readonly levels = computed(() => {
    const floor = rank(normalizeClearance(String(this.feed().config?.['classification'] ?? 'UNCLASSIFIED')));
    return levelsUpTo(this.auth.getClassificationLevel()).filter((l) => rank(l) >= floor);
  });

  ngOnInit(): void {
    this.level.set(this.levels()[0] ?? 'UNCLASSIFIED');
    this.reload();
  }

  protected reload(): void {
    this.service.list(this.feed().id).subscribe({
      next: (list) => this.tokens.set(list ?? []),
      error: (err) => this.error.set(messageOf(err)),
    });
  }

  protected issue(event: Event): void {
    event.preventDefault();
    this.error.set(null);
    const body: { name: string; classification: string; expiresAt?: string } = { name: this.name().trim(), classification: this.level() };
    if (this.expiresAt()) body.expiresAt = new Date(this.expiresAt()).toISOString();
    this.service.create(this.feed().id, body).subscribe({
      next: (created) => {
        this.secret.set(created.token);
        this.name.set('');
        this.reload();
      },
      error: (err) => this.error.set(messageOf(err)),
    });
  }

  protected revoke(t: IngestToken): void {
    if (this.confirming() !== t.id) {
      this.confirming.set(t.id);
      return;
    }
    this.confirming.set(null);
    this.service.revoke(this.feed().id, t.id).subscribe({ next: () => this.reload(), error: (err) => this.error.set(messageOf(err)) });
  }

  protected async copy(): Promise<void> {
    const s = this.secret();
    if (!s) return;
    try {
      await navigator.clipboard.writeText(s);
    } catch {
      // no clipboard (insecure origin, tests): the secret stays on screen to copy by hand
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
