import { ChangeDetectionStrategy, Component, ElementRef, HostListener, OnInit, afterRenderEffect, computed, effect, inject, signal, untracked, viewChild } from '@angular/core';
import { NgTemplateOutlet } from '@angular/common';
import { GeofenceService } from '../../core/services/geofence.service';
import { IconComponent, IconName } from '../../shared/components/icon.component';
import {
  GEOFENCE_COLORS,
  GeofenceDraft,
  GeofenceRule,
  GeofenceSeverity,
  GeofenceShape,
  GeofenceTrigger,
  WATCHABLE_TYPES,
} from '../../shared/models/geofence.model';
import { formatDistance, normalizeHex } from '../map/geofence-geometry';

const SHAPE_ICONS: Record<GeofenceShape, IconName> = { polygon: 'hexagon', rectangle: 'rectangle', circle: 'circle-dot', ring: 'ring' };
const SWATCH_NAMES = ['Red', 'Orange', 'Yellow', 'Green', 'Cyan', 'Blue', 'Purple'];

interface Editing {
  /** The zone being edited, or the draft just drawn. */
  base: GeofenceRule | GeofenceDraft;
  isDraft: boolean;
}

/**
 * The Geofences panel: every zone with its colour, shape, summary and switch;
 * an editor under the selected zone or the draft just drawn; New for writers.
 */
@Component({
  selector: 'app-geofences',
  standalone: true,
  imports: [IconComponent, NgTemplateOutlet],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './geofences.component.html',
  styleUrl: './geofences.component.scss',
})
export class GeofencesComponent implements OnInit {
  protected readonly service = inject(GeofenceService);

  protected readonly zones = this.service.zones;
  protected readonly selectedId = this.service.selectedId;
  protected readonly canWrite = this.service.canWrite;
  protected readonly activeCount = computed(() => this.zones().filter((z) => z.enabled).length);
  protected readonly swatches = GEOFENCE_COLORS.map((hex, i) => ({ hex, name: SWATCH_NAMES[i] }));
  protected readonly watchable = WATCHABLE_TYPES;
  protected readonly severities: GeofenceSeverity[] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW'];

  protected readonly editing = signal<Editing | null>(null);
  protected readonly name = signal('');
  protected readonly color = signal('#ef4444');
  protected readonly trigger = signal<GeofenceTrigger>('ENTRY');
  protected readonly types = signal<string[]>([]);
  protected readonly severity = signal<GeofenceSeverity>('HIGH');
  protected readonly saving = signal(false);
  /** Why the last write failed, shown until the next action. */
  protected readonly error = signal<string | null>(null);
  private openedId: string | null = null;

  private readonly nameField = viewChild<ElementRef<HTMLInputElement>>('nameField');
  private focusPending = false;

  constructor() {
    // A freshly drawn zone opens in the editor straight away.
    effect(() => {
      const draft = this.service.draft();
      if (draft && this.canWrite()) untracked(() => this.openEditor(draft, true));
    });
    // Selecting a zone, here or on the map, opens its editor for writers. Only a
    // change of selection does: the list updating underneath (a toggle, a drag,
    // another operator's save) must not throw away what is being typed.
    effect(() => {
      const id = this.selectedId();
      untracked(() => {
        if (id === this.openedId) return;
        this.openedId = id;
        const zone = this.zones().find((z) => z.id === id);
        if (zone && this.canWrite()) this.openEditor(zone, false);
        else if (!this.editing()?.isDraft) this.editing.set(null);
      });
    });
    afterRenderEffect(() => {
      const field = this.nameField();
      if (field && this.focusPending) {
        this.focusPending = false;
        field.nativeElement.focus();
      }
    });
  }

  ngOnInit(): void {
    this.service.load().subscribe({ error: () => undefined });
  }

  @HostListener('document:keydown', ['$event'])
  onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'n' || event.ctrlKey || event.metaKey || event.altKey) return;
    const tag = (event.target as HTMLElement | null)?.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
    if (!this.canWrite()) return;
    event.preventDefault();
    this.startNew();
  }

  protected startNew(): void {
    this.service.startDrawingRequested.next();
  }

  protected selectZone(zone: GeofenceRule): void {
    this.service.select(zone.id);
  }

  protected toggleEnabled(zone: GeofenceRule, event: Event): void {
    event.stopPropagation();
    this.error.set(null);
    this.service.setEnabled(zone.id, !zone.enabled).subscribe({ error: () => this.error.set('This geofence could not be changed. Check your access and try again.') });
  }

  protected shapeIcon(zone: GeofenceRule | GeofenceDraft): IconName {
    return SHAPE_ICONS[zone.config.shape] ?? 'hexagon';
  }

  protected shapeLine(zone: GeofenceRule | GeofenceDraft): string {
    const c = zone.config;
    switch (c.shape) {
      case 'circle': return `Circle · ${formatDistance(c.circle?.radiusM ?? 0)}`;
      case 'ring': return `Range ring · ${formatDistance(c.ring?.radiusM ?? 0)} · ${c.ring?.entityName ?? ''}`;
      case 'rectangle': return 'Rectangle';
      default: return `Polygon · ${c.polygon?.length ?? 0} pts`;
    }
  }

  protected isEditing(zone: GeofenceRule): boolean {
    const e = this.editing();
    return !!e && !e.isDraft && (e.base as GeofenceRule).id === zone.id;
  }

  protected setName(value: string): void { this.name.set(value); }
  protected setTrigger(value: GeofenceTrigger): void { this.trigger.set(value); }
  protected setSeverity(value: string): void { this.severity.set(value as GeofenceSeverity); }
  protected pickColor(hex: string): void { this.color.set(hex); }

  protected onHex(field: HTMLInputElement): void {
    const hex = normalizeHex(field.value);
    if (hex) this.color.set(hex);
    else field.value = this.color().replace('#', '');
  }

  protected toggleType(type: string): void {
    this.types.update((ts) => (ts.includes(type) ? ts.filter((t) => t !== type) : [...ts, type]));
  }

  protected watchEverything(): void {
    this.types.set([]);
  }

  protected save(): void {
    const e = this.editing();
    if (!e || this.saving()) return;
    this.saving.set(true);
    this.error.set(null);
    const failed = () => { this.saving.set(false); this.error.set('This geofence could not be saved. Check your access and try again.'); };
    if (e.isDraft) {
      const draft = e.base as GeofenceDraft;
      const body: GeofenceDraft = {
        ...draft,
        name: this.name().trim() || draft.name,
        config: { ...draft.config, color: this.color(), triggerOn: this.trigger() },
        monitoredEntityTypes: this.types(),
        severity: this.severity(),
      };
      this.service.create(body).subscribe({
        next: () => {
          this.saving.set(false);
          this.service.setDraft(null);
          this.closeEditor();
        },
        error: failed,
      });
      return;
    }
    // The zone as it is now: a drag or another save may have changed it since the editor opened.
    const opened = e.base as GeofenceRule;
    const zone = this.zones().find((z) => z.id === opened.id) ?? opened;
    const patch: Partial<GeofenceDraft> = {};
    const name = this.name().trim();
    if (name && name !== zone.name) patch.name = name;
    if (this.color() !== zone.config.color || this.trigger() !== zone.config.triggerOn) {
      patch.config = { ...zone.config, color: this.color(), triggerOn: this.trigger() };
    }
    if (!sameSet(this.types(), zone.monitoredEntityTypes)) patch.monitoredEntityTypes = this.types();
    if (this.severity() !== zone.severity) patch.severity = this.severity();
    if (Object.keys(patch).length === 0) {
      this.saving.set(false);
      this.closeEditor();
      return;
    }
    this.service.update(zone.id, patch).subscribe({
      next: () => { this.saving.set(false); this.closeEditor(); },
      error: failed,
    });
  }

  protected discard(): void {
    const e = this.editing();
    if (e?.isDraft) this.service.setDraft(null);
    this.closeEditor();
  }

  protected remove(zone: GeofenceRule): void {
    if (!confirm(`Delete the geofence "${zone.name}"? Its alerts stay.`)) return;
    this.error.set(null);
    this.service.delete(zone.id).subscribe({
      next: () => this.closeEditor(),
      error: () => this.error.set('This geofence could not be deleted. Check your access and try again.'),
    });
  }

  /** Close the editor and drop the selection, so the next click on the zone opens it again. */
  private closeEditor(): void {
    this.editing.set(null);
    this.service.select(null);
  }

  protected redraw(): void {
    const e = this.editing();
    if (e && !e.isDraft) this.service.redrawing.set(e.base as GeofenceRule);
    this.service.startDrawingRequested.next();
  }

  private openEditor(base: GeofenceRule | GeofenceDraft, isDraft: boolean): void {
    this.error.set(null);
    this.editing.set({ base, isDraft });
    this.name.set(base.name);
    this.color.set(base.config.color);
    this.trigger.set(base.config.triggerOn);
    this.types.set([...base.monitoredEntityTypes]);
    this.severity.set(base.severity);
    this.focusPending = true;
  }
}

function sameSet(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((x) => b.includes(x));
}
