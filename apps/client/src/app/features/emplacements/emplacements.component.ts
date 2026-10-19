import { ChangeDetectionStrategy, Component, OnDestroy, OnInit, computed, effect, inject, signal, untracked } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { NgTemplateOutlet } from '@angular/common';
import { EmplacementService, errorMessage } from '../../core/services/emplacement.service';
import { FeedService } from '../../core/services/feed.service';
import { AuthService } from '../../core/services/auth.service';
import { IconComponent } from '../../shared/components/icon.component';
import { directionOf } from '../../shared/models/feed.model';
import { levelsUpTo } from '../../shared/models/classification';
import {
  Emplacement,
  EmplacementCoverage,
  EmplacementDraft,
  Mobility,
  SENSOR_TYPES,
  SENSOR_TYPE_LABELS,
  SensorType,
  coverageSummary,
  draftOf,
  inputOf,
  newDraft,
} from '../../shared/models/emplacement.model';

const CHANGED = 'Changed by someone else since you opened it. The latest is loaded; review and save again.';
const DELETED = 'Deleted by someone else.';

/** A coordinate input's text as a number, or null when it is empty or not a number yet. */
function parseCoordinate(text: string): number | null {
  if (text.trim() === '') return null;
  const n = Number(text);
  return Number.isFinite(n) ? n : null;
}

/**
 * The Emplacements panel: every emplacement this user may see, and an editor
 * for a new one or the selected one. Position can be typed or picked on the
 * map; the map previews the editor's coverage as it changes.
 */
@Component({
  selector: 'app-emplacements',
  standalone: true,
  imports: [IconComponent, NgTemplateOutlet],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './emplacements.component.html',
  styleUrl: './emplacements.component.scss',
})
export class EmplacementsComponent implements OnInit, OnDestroy {
  protected readonly service = inject(EmplacementService);
  private readonly feedService = inject(FeedService);
  private readonly auth = inject(AuthService);

  protected readonly list = this.service.emplacements;
  protected readonly selectedId = this.service.selectedId;
  protected readonly draft = this.service.draft;
  protected readonly picking = this.service.picking;
  protected readonly canWrite = this.service.canWrite;
  protected readonly canDelete = this.service.canDelete;
  protected readonly showCoverage = this.service.showCoverage;
  protected readonly sensorTypes = SENSOR_TYPES;
  protected readonly sensorLabels = SENSOR_TYPE_LABELS;
  protected readonly levels = computed(() => levelsUpTo(this.auth.getClassificationLevel()));
  /** Connections that take data in, apart from the built-in mirror. */
  protected readonly connections = computed(() =>
    this.feedService.feeds().filter((f) => directionOf(f) !== 'out' && f.driver !== 'emplacement'),
  );
  protected readonly filter = signal<'ALL' | Mobility>('ALL');
  protected readonly sensorFilter = signal<'ALL' | SensorType>('ALL');
  protected readonly activeFilter = signal<'ALL' | 'ACTIVE' | 'INACTIVE'>('ALL');
  protected readonly shown = computed(() => {
    const mobility = this.filter();
    const sensor = this.sensorFilter();
    const active = this.activeFilter();
    return this.list().filter(
      (e) =>
        (mobility === 'ALL' || e.mobility === mobility) &&
        (sensor === 'ALL' || e.sensorType === sensor) &&
        (active === 'ALL' || e.active === (active === 'ACTIVE')),
    );
  });
  /**
   * The editor sits above the list for a new emplacement, and for one whose
   * row is not shown: filtered out, or deleted by someone else while open.
   */
  protected readonly editorOnTop = computed(() => {
    const d = this.draft();
    return !!d && (d.id === null || !this.shown().some((e) => e.id === d.id));
  });
  protected readonly saving = signal(false);
  protected readonly error = signal<string | null>(null);
  /** The coordinate inputs' text, so a half-typed value ("-") is not lost to the draft. */
  protected readonly latText = signal('');
  protected readonly lonText = signal('');
  private readonly draftPosition = computed(() => this.draft()?.position ?? null);

  constructor() {
    // A position set from outside (the map pick) shows in the inputs.
    effect(() => {
      const p = this.draftPosition();
      if (!p) return;
      untracked(() => {
        if (parseCoordinate(this.latText()) !== p.lat) this.latText.set(String(p.lat));
        if (parseCoordinate(this.lonText()) !== p.lon) this.lonText.set(String(p.lon));
      });
    });
  }

  ngOnInit(): void {
    this.service.load().subscribe({ error: () => this.error.set('Could not load emplacements.') });
    if (this.feedService.feeds().length === 0) this.feedService.load().subscribe({ error: () => undefined });
  }

  ngOnDestroy(): void {
    this.picking.set(false);
    this.draft.set(null);
  }

  protected startNew(): void {
    this.error.set(null);
    this.service.select(null);
    this.seed(newDraft());
  }

  protected edit(e: Emplacement): void {
    this.error.set(null);
    this.service.select(e.id);
    this.seed(draftOf(e));
  }

  private seed(d: EmplacementDraft): void {
    this.latText.set(d.position ? String(d.position.lat) : '');
    this.lonText.set(d.position ? String(d.position.lon) : '');
    this.draft.set(d);
  }

  protected cancel(): void {
    this.draft.set(null);
    this.picking.set(false);
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

  protected save(): void {
    const d = this.draft();
    if (!d) return;
    this.error.set(null);
    // The draft keeps the version the user opened. The list may hold a newer
    // one (the map refreshes it), and saving with that would overwrite
    // someone else's change instead of being refused.
    if (d.id && !this.list().some((e) => e.id === d.id)) {
      this.error.set(DELETED);
      return;
    }
    this.saving.set(true);
    const request = d.id && d.version != null ? this.service.update(d.id, d.version, inputOf(d)) : this.service.create(inputOf(d));
    request.subscribe({
      next: (e) => {
        this.saving.set(false);
        this.draft.set(null);
        this.service.select(e.id);
      },
      error: (err) => {
        this.saving.set(false);
        if (err instanceof HttpErrorResponse && err.status === 412 && d.id) {
          this.reloadAfterConflict(d.id);
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

  protected remove(e: Emplacement): void {
    this.error.set(null);
    this.service.remove(e.id).subscribe({
      next: () => this.draft.set(null),
      error: (err) => this.error.set(errorMessage(err)),
    });
  }

  protected summary(e: Emplacement): string {
    return `${SENSOR_TYPE_LABELS[e.sensorType] ?? e.sensorType} · ${e.mobility.toLowerCase()} · ${coverageSummary(e.coverage)}`;
  }

  protected km(m: number): number {
    return Math.round(m / 100) / 10;
  }

  protected selectedEmplacement(id: string): Emplacement | undefined {
    return this.list().find((e) => e.id === id);
  }
}
