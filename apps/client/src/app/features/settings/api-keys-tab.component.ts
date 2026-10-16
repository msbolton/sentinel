import { ChangeDetectionStrategy, Component, OnInit, inject, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { ApiKeyService, CreatedApiKey } from '../../core/services/api-key.service';
import { AuthService } from '../../core/services/auth.service';
import { levelsUpTo, normalizeClearance } from '../../shared/models/classification';

/**
 * Personal API keys for GIS tools and KML network links. The secret is
 * shown once, right after the key is created, and never again.
 */
@Component({
  selector: 'app-api-keys-tab',
  standalone: true,
  imports: [DatePipe, ReactiveFormsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="api-keys">
      <p class="intro">
        Keys let GIS tools and KML network links read exports as you, with <code>?api_key=</code> on
        <code>/api/v1/export/...</code>. They are read-only and never see more than your clearance.
      </p>

      @if (error(); as err) {
        <div class="toast error" role="alert">{{ err }}</div>
      }

      @if (created(); as c) {
        <div class="reveal" role="status">
          <div class="reveal-head">
            <span>Key <strong>{{ c.name }}</strong> created. Copy the secret now; it is not shown again.</span>
            <button type="button" class="btn ghost" (click)="dismissSecret()">Done</button>
          </div>
          <div class="reveal-secret">
            <input #secretInput type="text" class="monospace" readonly [value]="c.secret" (focus)="secretInput.select()" aria-label="API key secret" />
            <button type="button" class="btn" (click)="copy(c.secret, secretInput)">{{ copied() ? 'Copied' : 'Copy' }}</button>
          </div>
        </div>
      }

      <div class="panel">
        <div class="panel-header">
          <h2>Your keys</h2>
          <button type="button" class="refresh-btn" (click)="refresh()">Refresh</button>
        </div>
        @if (keys().length === 0) {
          <div class="empty">No keys yet.</div>
        } @else {
          <table class="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Prefix</th>
                <th>Classification</th>
                <th>Created</th>
                <th>Expires</th>
                <th>Last used</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              @for (key of keys(); track key.id) {
                <tr>
                  <td>{{ key.name }}</td>
                  <td class="monospace">{{ key.prefix }}…</td>
                  <td><span class="classification-badge">{{ key.classification }}</span></td>
                  <td>{{ key.createdAt | date: 'short' }}</td>
                  <td>{{ key.expiresAt ? (key.expiresAt | date: 'short') : 'never' }}</td>
                  <td>{{ key.lastUsedAt ? (key.lastUsedAt | date: 'short') : 'never' }}</td>
                  <td class="actions">
                    @if (confirmingRevoke() === key.id) {
                      <span class="confirm">Revoke?</span>
                      <button type="button" class="btn reject" [disabled]="revoking() === key.id" (click)="confirmRevoke(key.id)">
                        {{ revoking() === key.id ? 'Revoking…' : 'Yes' }}
                      </button>
                      <button type="button" class="btn ghost" [disabled]="revoking() === key.id" (click)="cancelRevoke()">No</button>
                    } @else {
                      <button type="button" class="btn ghost revoke" (click)="askRevoke(key.id)">Revoke</button>
                    }
                  </td>
                </tr>
              }
            </tbody>
          </table>
        }
      </div>

      <div class="panel">
        <div class="panel-header">
          <h2>Create key</h2>
        </div>
        <form class="create-form" [formGroup]="form" (ngSubmit)="create()" novalidate>
          <label class="field">
            <span>Name</span>
            <input type="text" formControlName="name" placeholder="e.g. QGIS on the ops laptop" autocomplete="off" />
          </label>
          <label class="field">
            <span>Classification</span>
            <select formControlName="classification">
              @for (level of classificationOptions; track level) {
                <option [value]="level">{{ level }}</option>
              }
            </select>
          </label>
          <label class="field">
            <span>Expires in (days)</span>
            <input type="number" formControlName="expiresInDays" min="1" step="1" placeholder="never" />
          </label>
          <button type="submit" class="btn approve" [disabled]="form.invalid || creating()">
            {{ creating() ? 'Creating…' : 'Create key' }}
          </button>
        </form>
      </div>
    </div>
  `,
  styles: [`
    :host {
      display: block;
    }

    .intro {
      margin: 0 0 16px;
      font-size: 13px;
      color: #b0b0cc;

      code {
        font-family: 'JetBrains Mono', 'Fira Code', monospace;
        color: #e0e0e0;
      }
    }

    .panel {
      background: #1a1a2e;
      border: 1px solid #2a2a3e;
      border-radius: 8px;
      padding: 20px;
      margin-bottom: 20px;
    }

    .panel-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 16px;

      h2 {
        margin: 0;
        font-size: 16px;
        font-weight: 600;
        color: #ffffff;
      }
    }

    .refresh-btn {
      padding: 6px 12px;
      background: #2a2a3e;
      border: 1px solid #3a3a5e;
      border-radius: 4px;
      color: #b0b0cc;
      cursor: pointer;
      font-size: 12px;

      &:hover {
        background: #3a3a5e;
      }
    }

    .empty {
      padding: 20px;
      text-align: center;
      color: #6666aa;
      font-size: 13px;
    }

    .toast {
      padding: 10px 16px;
      border-radius: 6px;
      margin-bottom: 16px;
      font-size: 13px;

      &.error {
        background: #ff4a4a22;
        border: 1px solid #ff4a4a44;
        color: #ff6b6b;
      }
    }

    .reveal {
      padding: 12px 16px;
      border-radius: 6px;
      margin-bottom: 16px;
      font-size: 13px;
      background: #4aff4a12;
      border: 1px solid #4aff4a44;
      color: #c0ffc0;
    }

    .reveal-head {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
      margin-bottom: 8px;
    }

    .reveal-secret {
      display: flex;
      gap: 8px;

      input {
        flex: 1;
        padding: 6px 10px;
        background: #0e0e1a;
        border: 1px solid #2a2a3e;
        border-radius: 4px;
        color: #e0e0e0;
        font-size: 13px;
      }
    }

    .monospace {
      font-family: 'JetBrains Mono', 'Fira Code', monospace;
    }

    .classification-badge {
      display: inline-block;
      padding: 2px 8px;
      background: #4a9eff22;
      border: 1px solid #4a9eff44;
      border-radius: 4px;
      font-size: 12px;
      font-weight: 600;
      color: #4a9eff;
    }

    .data-table {
      width: 100%;
      border-collapse: collapse;
      font-size: 13px;

      th {
        text-align: left;
        padding: 8px 12px;
        border-bottom: 1px solid #2a2a3e;
        color: #6666aa;
        font-size: 11px;
        text-transform: uppercase;
        letter-spacing: 0.5px;
      }

      td {
        padding: 10px 12px;
        border-bottom: 1px solid #1e1e32;
        color: #c0c0dd;
      }
    }

    .actions {
      display: flex;
      align-items: center;
      gap: 6px;
      white-space: nowrap;
    }

    .confirm {
      font-size: 12px;
      color: #ff6b6b;
    }

    .create-form {
      display: flex;
      flex-wrap: wrap;
      align-items: flex-end;
      gap: 12px;
    }

    .field {
      display: flex;
      flex-direction: column;
      gap: 4px;
      min-width: 160px;

      span {
        font-size: 11px;
        text-transform: uppercase;
        color: #6666aa;
        letter-spacing: 0.5px;
      }

      input,
      select {
        padding: 6px 10px;
        background: #0e0e1a;
        border: 1px solid #2a2a3e;
        border-radius: 4px;
        color: #e0e0e0;
        font-size: 13px;
      }
    }

    .btn {
      padding: 6px 12px;
      border: none;
      border-radius: 4px;
      font-size: 12px;
      cursor: pointer;
      background: #2a2a3e;
      color: #e0e0e0;
      transition: background 0.2s;

      &:disabled {
        opacity: 0.5;
        cursor: not-allowed;
      }

      &.ghost {
        background: transparent;
        color: #8888aa;

        &:hover:not(:disabled) {
          background: #2a2a3e;
        }
      }

      &.revoke:hover:not(:disabled) {
        color: #ff6b6b;
      }

      &.approve {
        background: #2a7a2a;
        color: #ffffff;

        &:hover:not(:disabled) {
          background: #3a9a3a;
        }
      }

      &.reject {
        background: #7a2a2a;
        color: #ffffff;

        &:hover:not(:disabled) {
          background: #9a3a3a;
        }
      }
    }
  `],
})
export class ApiKeysTabComponent implements OnInit {
  private readonly apiKeys = inject(ApiKeyService);
  private readonly auth = inject(AuthService);

  readonly keys = this.apiKeys.keys;
  readonly clearance = normalizeClearance(this.auth.getClassificationLevel());
  readonly classificationOptions = levelsUpTo(this.clearance);

  readonly creating = signal(false);
  readonly error = signal<string | null>(null);
  /** The key just created, secret included; cleared on Done or when the tab goes away. */
  readonly created = signal<CreatedApiKey | null>(null);
  readonly copied = signal(false);
  readonly confirmingRevoke = signal<string | null>(null);
  readonly revoking = signal<string | null>(null);

  readonly form = new FormGroup({
    name: new FormControl('', { nonNullable: true, validators: [Validators.required, Validators.maxLength(80)] }),
    classification: new FormControl<string>('UNCLASSIFIED', { nonNullable: true }),
    expiresInDays: new FormControl<number | null>(null, [Validators.min(1), Validators.max(3650)]),
  });

  ngOnInit(): void {
    if (this.apiKeys.available() === null) {
      this.refresh();
    }
  }

  refresh(): void {
    this.error.set(null);
    this.apiKeys.list().subscribe({ error: (err) => this.error.set(messageOf(err, 'Could not load your keys.')) });
  }

  create(): void {
    if (this.form.invalid || this.creating()) {
      this.form.markAllAsTouched();
      return;
    }
    const v = this.form.getRawValue();
    this.creating.set(true);
    this.error.set(null);
    this.copied.set(false);
    this.apiKeys
      .create({
        name: v.name.trim(),
        classification: v.classification,
        expiresInDays: typeof v.expiresInDays === 'number' && v.expiresInDays > 0 ? Math.floor(v.expiresInDays) : undefined,
      })
      .subscribe({
        next: (key) => {
          this.creating.set(false);
          this.created.set(key);
          this.form.reset({ name: '', classification: v.classification, expiresInDays: null });
        },
        error: (err) => {
          this.creating.set(false);
          this.error.set(messageOf(err, 'The key could not be created.'));
        },
      });
  }

  copy(secret: string, input: HTMLInputElement): void {
    const clipboard = typeof navigator !== 'undefined' ? navigator.clipboard : undefined;
    if (clipboard?.writeText) {
      clipboard.writeText(secret).then(
        () => this.copied.set(true),
        () => this.selectForManualCopy(input),
      );
    } else {
      this.selectForManualCopy(input);
    }
  }

  dismissSecret(): void {
    this.created.set(null);
    this.copied.set(false);
  }

  askRevoke(id: string): void {
    this.confirmingRevoke.set(id);
  }

  cancelRevoke(): void {
    this.confirmingRevoke.set(null);
  }

  confirmRevoke(id: string): void {
    this.revoking.set(id);
    this.error.set(null);
    this.apiKeys.revoke(id).subscribe({
      next: () => {
        this.revoking.set(null);
        this.confirmingRevoke.set(null);
        if (this.created()?.id === id) {
          this.dismissSecret();
        }
      },
      error: (err) => {
        this.revoking.set(null);
        this.confirmingRevoke.set(null);
        this.error.set(messageOf(err, 'The key could not be revoked.'));
      },
    });
  }

  private selectForManualCopy(input: HTMLInputElement): void {
    this.copied.set(false);
    input.focus();
    input.select();
  }
}

function messageOf(err: unknown, fallback: string): string {
  const body = (err as { error?: { error?: string; message?: string } })?.error;
  return body?.error || body?.message || fallback;
}
