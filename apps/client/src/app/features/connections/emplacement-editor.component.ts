import { ChangeDetectionStrategy, Component, OnDestroy, computed, effect, inject, input, output, signal, untracked } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Router } from '@angular/router';
import { EmplacementService, errorMessage } from '../../core/services/emplacement.service';
import { AuthService } from '../../core/services/auth.service';
import { IconComponent } from '../../shared/components/icon.component';
import { Feed } from '../../shared/models/feed.model';
import { levelsUpTo, normalizeClearance, rank } from '../../shared/models/classification';
import { EmplacementCoverage, EmplacementDraft, SENSOR_TYPES, SENSOR_TYPE_LABELS, inputOf } from '../../shared/models/emplacement.model';

const CHANGED = 'Changed by someone else since you opened it. The latest is loaded; review and save again.';
const DELETED = 'Deleted by someone else.';

/** A coordinate input's text as a number, or null when it is empty or not a number yet. */
function parseCoordinate(text: string): number | null {
  if (text.trim() === '') return null;
  const n = Number(text);
  return Number.isFinite(n) ? n : null;
}

/**
 * The editor of one emplacement, inside its connection's row on the
 * Connections page. It edits the service's draft, which the map previews,
 * and is locked to the connection:
 * - its emplacements are bound to it;
 * - an exercise connection's are exercise;
 * - none is below the connection's own level.
 * It clears the draft when it goes.
 */
@Component({
  selector: 'app-emplacement-editor',
  standalone: true,
  imports: [IconComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './emplacement-editor.component.html',
  styleUrl: './emplacement-editor.component.scss',
})
export class EmplacementEditorComponent implements OnDestroy {
  readonly feed = input.required<Feed>();
  readonly closed = output<void>();

  protected readonly service = inject(EmplacementService);
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);

  protected readonly draft = this.service.draft;
  protected readonly canWrite = this.service.canWrite;
  protected readonly sensorTypes = SENSOR_TYPES;
  protected readonly sensorLabels = SENSOR_TYPE_LABELS;
  /** An exercise connection's emplacements are exercise. */
  protected readonly exerciseLocked = computed(() => this.feed().config?.['exercise'] === true);
  /** The connection's own level: no emplacement of it is below. */
  private readonly floor = computed(() => normalizeClearance(String(this.feed().config?.['classification'] ?? 'UNCLASSIFIED')));
  /** The levels the caller may set, from the connection's own up to their clearance. */
  protected readonly levels = computed(() => levelsUpTo(this.auth.getClassificationLevel()).filter((l) => rank(l) >= rank(this.floor())));
  protected readonly saving = signal(false);
  protected readonly error = signal<string | null>(null);
  /** The coordinate inputs' text, so a half-typed value ("-") is not lost to the draft. */
  protected readonly latText = signal('');
  protected readonly lonText = signal('');

  constructor() {
    // The inputs follow a position set from outside (opening another draft).
    effect(() => {
      const p = this.draft()?.position;
      if (!p) return;
      untracked(() => {
        if (parseCoordinate(this.latText()) !== p.lat) this.latText.set(String(p.lat));
        if (parseCoordinate(this.lonText()) !== p.lon) this.lonText.set(String(p.lon));
      });
    });
  }

  ngOnDestroy(): void {
    this.draft.set(null);
  }

  protected patch(change: Partial<EmplacementDraft>): void {
    this.draft.update((d) => (d ? { ...d, ...change } : d));
  }

  protected patchCoverage(change: Partial<EmplacementCoverage>): void {
    this.draft.update((d) => (d ? { ...d, coverage: { ...d.coverage, ...change } } : d));
  }

  /** The draft's position follows the inputs only while both hold a number, or both are empty. */
  protected typeCoordinate(axis: 'lat' | 'lon', event: Event): void {
    (axis === 'lat' ? this.latText : this.lonText).set(this.text(event));
    const lat = parseCoordinate(this.latText());
    const lon = parseCoordinate(this.lonText());
    if (lat != null && lon != null) this.patch({ position: { lat, lon } });
    else if (this.latText().trim() === '' && this.lonText().trim() === '') this.patch({ position: null });
  }

  protected text(event: Event): string {
    return (event.target as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement).value;
  }

  protected num(event: Event): number | null {
    const raw = (event.target as HTMLInputElement).value;
    return raw === '' || Number.isNaN(Number(raw)) ? null : Number(raw);
  }

  protected checked(event: Event): boolean {
    return (event.target as HTMLInputElement).checked;
  }

  protected km(m: number): number {
    return Math.round(m / 100) / 10;
  }

  protected save(): void {
    const d = this.draft();
    if (!d) return;
    this.error.set(null);
    const locked: EmplacementDraft = {
      ...d,
      feedId: this.feed().id,
      exercise: d.exercise || this.exerciseLocked(),
      classification: rank(d.classification) < rank(this.floor()) ? this.floor() : d.classification,
    };
    // The draft keeps the version the user opened; a newer one on the server answers 412.
    if (locked.id && !this.service.emplacements().some((e) => e.id === locked.id)) {
      this.error.set(DELETED);
      return;
    }
    this.saving.set(true);
    const request =
      locked.id && locked.version != null ? this.service.update(locked.id, locked.version, inputOf(locked)) : this.service.create(inputOf(locked));
    request.subscribe({
      next: () => {
        this.saving.set(false);
        this.draft.set(null);
        this.closed.emit();
      },
      error: (err) => {
        this.saving.set(false);
        if (err instanceof HttpErrorResponse && err.status === 412 && locked.id) {
          this.reloadAfterConflict(locked.id);
        } else {
          this.error.set(errorMessage(err));
        }
      },
    });
  }

  /** Someone saved first: load the latest and let the next save claim it, keeping the user's edits. */
  private reloadAfterConflict(id: string): void {
    this.error.set(CHANGED);
    this.service.load().subscribe({
      next: (list) => {
        const latest = list.find((e) => e.id === id);
        if (!latest) {
          this.error.set(DELETED);
          return;
        }
        this.draft.update((d) => (d?.id === id ? { ...d, version: latest.version } : d));
      },
      error: () => undefined,
    });
  }

  protected cancel(): void {
    this.draft.set(null);
    this.closed.emit();
  }

  /** Flies the map to the typed position; the draft stays here, and goes with the editor. */
  protected showOnMap(): void {
    const p = this.draft()?.position;
    if (!p) return;
    this.service.focus.set({ ...p });
    void this.router.navigateByUrl('/map');
  }
}
