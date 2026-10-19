import { ChangeDetectionStrategy, Component, OnDestroy, OnInit, computed, inject, signal } from '@angular/core';
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
  coverageSummary,
  draftOf,
  inputOf,
  newDraft,
} from '../../shared/models/emplacement.model';

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
  protected readonly shown = computed(() => {
    const f = this.filter();
    return f === 'ALL' ? this.list() : this.list().filter((e) => e.mobility === f);
  });
  protected readonly saving = signal(false);
  protected readonly error = signal<string | null>(null);

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
    this.draft.set(newDraft());
  }

  protected edit(e: Emplacement): void {
    this.error.set(null);
    this.service.select(e.id);
    this.draft.set(draftOf(e));
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

  protected patchPosition(axis: 'lat' | 'lon', value: number | null): void {
    this.draft.update((d) => {
      if (!d) return d;
      if (value == null) return { ...d, position: null };
      const current = d.position ?? { lat: 0, lon: 0 };
      return { ...d, position: { ...current, [axis]: value } };
    });
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
    this.saving.set(true);
    this.error.set(null);
    const existing = d.id ? this.list().find((e) => e.id === d.id) : undefined;
    const request = existing ? this.service.update(existing, inputOf(d)) : this.service.create(inputOf(d));
    request.subscribe({
      next: (e) => {
        this.saving.set(false);
        this.draft.set(null);
        this.service.select(e.id);
      },
      error: (err) => {
        this.saving.set(false);
        this.error.set(errorMessage(err));
      },
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
