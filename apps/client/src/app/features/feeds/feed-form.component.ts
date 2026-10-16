import {
  ChangeDetectionStrategy,
  Component,
  HostListener,
  computed,
  effect,
  inject,
  input,
  output,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { map } from 'rxjs';
import { FeedService, problemsOf } from '../../core/services/feed.service';
import { CreateFeedRequest, Feed, FeedProblem, ReplaceFeedRequest } from '../../shared/models/feed.model';
import { SchemaFormChange, SchemaFormComponent } from '../../shared/components/schema-form/schema-form.component';

export const FEED_ID_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}$/;

const EMPTY_CONFIG: Record<string, unknown> = {};
const EMPTY_SECRETS: Record<string, string> = {};

/** The slug the server would derive from a name when no id is given. */
export function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63)
    .replace(/-+$/g, '');
}

/**
 * Right-side drawer that creates a feed (pick a driver, then fill its
 * options) or edits an existing one (driver fixed). Server validation
 * problems are routed to the field they name.
 */
@Component({
  selector: 'app-feed-form',
  standalone: true,
  imports: [ReactiveFormsModule, SchemaFormComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './feed-form.component.html',
  styleUrl: './feed-form.component.scss',
})
export class FeedFormComponent {
  private readonly feedService = inject(FeedService);

  readonly mode = input.required<'create' | 'edit'>();
  readonly feed = input<Feed | null>(null);

  readonly closed = output<void>();
  readonly saved = output<Feed>();

  readonly schemaForm = viewChild(SchemaFormComponent);

  readonly drivers = this.feedService.drivers;
  readonly selectedDriverId = signal<string | null>(null);
  readonly driver = computed(() => {
    const id = this.selectedDriverId();
    return id ? this.drivers().find((d) => d.id === id) : undefined;
  });

  readonly step = signal<1 | 2>(1);
  readonly saving = signal(false);
  readonly error = signal<string | null>(null);
  readonly problems = signal<FeedProblem[]>([]);
  readonly submitted = signal(false);

  readonly form = new FormGroup({
    name: new FormControl('', { nonNullable: true, validators: [Validators.required, Validators.maxLength(120)] }),
    id: new FormControl('', { nonNullable: true, validators: [Validators.pattern(FEED_ID_PATTERN)] }),
    description: new FormControl('', { nonNullable: true, validators: [Validators.maxLength(500)] }),
    enabled: new FormControl(true, { nonNullable: true }),
  });

  private readonly formValue = toSignal(this.form.valueChanges.pipe(map(() => this.form.getRawValue())), {
    initialValue: this.form.getRawValue(),
  });
  private readonly formStatus = toSignal(this.form.statusChanges, { initialValue: this.form.status });

  private readonly schemaState = signal<SchemaFormChange>({ config: {}, secretRefs: {}, valid: true });

  readonly idPreview = computed(() => {
    const v = this.formValue();
    const explicit = v.id.trim();
    return explicit || slugify(v.name) || '—';
  });

  readonly canSubmit = computed(
    () => this.formStatus() === 'VALID' && this.schemaState().valid && !this.saving() && !!this.driver(),
  );

  /** Problems the schema form owns. */
  readonly schemaProblems = computed(() =>
    this.problems().filter((p) => p.path.startsWith('/config') || p.path.startsWith('/secretRefs')),
  );
  /** Problems that name neither a top-level field nor the config. */
  readonly otherProblems = computed(() =>
    this.problems().filter(
      (p) => !p.path.startsWith('/config') && !p.path.startsWith('/secretRefs') && !TOP_LEVEL_FIELDS.has(p.path),
    ),
  );

  /** The config the schema form starts from: the feed's own, or nothing. */
  readonly initialConfig = computed(() => this.feed()?.config ?? EMPTY_CONFIG);
  readonly initialSecretRefs = computed(() => this.feed()?.secretRefs ?? EMPTY_SECRETS);

  readonly title = computed(() => {
    if (this.mode() === 'edit') {
      return `Edit ${this.feed()?.name ?? 'feed'}`;
    }
    return this.step() === 1 ? 'Add feed: choose a driver' : `Add feed: ${this.driver()?.name ?? ''}`;
  });

  constructor() {
    effect(() => {
      const mode = this.mode();
      const feed = this.feed();
      untracked(() => this.reset(mode, feed));
    });
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    if (!this.saving()) {
      this.close();
    }
  }

  close(): void {
    this.closed.emit();
  }

  selectDriver(id: string): void {
    this.selectedDriverId.set(id);
    this.problems.set([]);
    this.error.set(null);
    this.step.set(2);
  }

  back(): void {
    if (this.mode() === 'create' && !this.saving()) {
      this.step.set(1);
    }
  }

  onSchemaChange(change: SchemaFormChange): void {
    this.schemaState.set(change);
  }

  /** Whether to show a top-level field's client-side error. */
  showError(field: 'name' | 'id' | 'description'): boolean {
    this.formStatus();
    const control = this.form.controls[field];
    return control.invalid && (control.touched || control.dirty || this.submitted());
  }

  touch(): void {
    this.submitted.update((v) => v); // no-op write keeps OnPush templates honest after blur
    this.form.updateValueAndValidity({ onlySelf: true, emitEvent: true });
  }

  problemsFor(field: 'name' | 'id' | 'description' | 'enabled' | 'driver'): string[] {
    return this.problems()
      .filter((p) => p.path === `/${field}`)
      .map((p) => p.message);
  }

  submit(): void {
    this.submitted.set(true);
    this.error.set(null);
    const driver = this.driver();
    if (!driver) {
      this.error.set('Pick a driver first.');
      return;
    }
    if (!this.canSubmit()) {
      this.form.markAllAsTouched();
      this.schemaForm()?.markAllTouched();
      this.error.set('Fix the highlighted fields before saving.');
      return;
    }

    const v = this.form.getRawValue();
    const { config, secretRefs } = this.schemaState();
    this.saving.set(true);
    this.problems.set([]);

    const request =
      this.mode() === 'edit' && this.feed()
        ? this.feedService.replace(this.feed()!.id, {
            name: v.name.trim(),
            description: v.description.trim(),
            config,
            secretRefs,
            enabled: v.enabled,
          } satisfies ReplaceFeedRequest)
        : this.feedService.create({
            id: v.id.trim() || undefined,
            name: v.name.trim(),
            description: v.description.trim() || undefined,
            driver: driver.id,
            config,
            secretRefs,
            enabled: v.enabled,
          } satisfies CreateFeedRequest);

    request.subscribe({
      next: (feed) => {
        this.saving.set(false);
        this.saved.emit(feed);
        this.closed.emit();
      },
      error: (err: unknown) => {
        this.saving.set(false);
        const { message, problems } = problemsOf(err);
        this.problems.set(problems);
        const status = (err as { status?: number })?.status;
        if (status === 409) {
          this.error.set(`A feed with id "${this.idPreview()}" already exists. ${message}`.trim());
        } else if (status === 403) {
          this.error.set('You do not have permission to change feeds.');
        } else if (status === 422 && problems.length > 0) {
          this.error.set(`${message}: check the highlighted fields.`);
        } else {
          this.error.set(message || 'The feed could not be saved.');
        }
        this.schemaForm()?.markAllTouched();
      },
    });
  }

  private reset(mode: 'create' | 'edit', feed: Feed | null): void {
    this.problems.set([]);
    this.error.set(null);
    this.saving.set(false);
    this.submitted.set(false);
    if (mode === 'edit' && feed) {
      this.selectedDriverId.set(feed.driver);
      this.step.set(2);
      this.form.reset({ name: feed.name, id: feed.id, description: feed.description ?? '', enabled: feed.enabled });
      this.form.controls.id.disable();
    } else {
      this.selectedDriverId.set(null);
      this.step.set(1);
      this.form.reset({ name: '', id: '', description: '', enabled: true });
      this.form.controls.id.enable();
    }
  }
}

const TOP_LEVEL_FIELDS = new Set(['/name', '/id', '/description', '/enabled', '/driver']);
