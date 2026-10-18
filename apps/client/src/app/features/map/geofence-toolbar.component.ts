import { ChangeDetectionStrategy, Component, HostListener, computed, input, output } from '@angular/core';
import { IconComponent, IconName } from '../../shared/components/icon.component';
import { GEOFENCE_COLORS, GeofenceShape } from '../../shared/models/geofence.model';
import { normalizeHex } from './geofence-geometry';
import { DrawSession, canFinish, liveMeasure } from './geofence-draw-state';

const SHAPES: { shape: GeofenceShape; label: string; icon: IconName; hint: string }[] = [
  { shape: 'polygon', label: 'Polygon', icon: 'hexagon', hint: 'Click to add a point · double-click to close · Esc cancels' },
  { shape: 'rectangle', label: 'Rectangle', icon: 'rectangle', hint: 'Click one corner, then the opposite corner' },
  { shape: 'circle', label: 'Circle', icon: 'circle-dot', hint: 'Click the origin, then click to set the radius' },
  { shape: 'ring', label: 'Range ring', icon: 'ring', hint: 'Click a track to anchor the ring, then click to set the radius' },
];

const SWATCH_NAMES = ['Red', 'Orange', 'Yellow', 'Green', 'Cyan', 'Blue', 'Purple'];

/**
 * The bar that floats over the map while a geofence is being drawn: shape,
 * colour, radius for the round shapes, the hint for the current shape, and
 * Finish or Cancel. Enter finishes and Escape cancels from anywhere but a field.
 */
@Component({
  selector: 'app-geofence-toolbar',
  standalone: true,
  imports: [IconComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="toolbar" role="toolbar" aria-label="Geofence drawing">
      <span class="eyebrow">New geofence</span>
      <span class="rule"></span>

      <div class="shapes" role="group" aria-label="Shape">
        @for (s of shapes; track s.shape) {
          <button type="button" class="shape" [class.active]="s.shape === session().shape" [attr.aria-pressed]="s.shape === session().shape" (click)="shapeChange.emit(s.shape)">
            <app-icon [name]="s.icon" />{{ s.label }}
          </button>
        }
      </div>
      <span class="rule"></span>

      <div class="colours" role="group" aria-label="Colour">
        @for (c of swatches; track c.hex) {
          <button type="button" class="swatch" [class.active]="c.hex === session().color" [style.background]="c.hex" [style.--swatch]="c.hex" [attr.aria-label]="c.name" [attr.aria-pressed]="c.hex === session().color" (click)="colorChange.emit(c.hex)"></button>
        }
        <label class="hex">
          <span class="chip" [style.background]="session().color"></span>
          <span aria-hidden="true">#</span>
          <input #hexField type="text" [value]="hexValue()" aria-label="Custom colour hex" spellcheck="false" (change)="onHex(hexField)">
        </label>
      </div>

      @if (session().shape === 'circle' || session().shape === 'ring') {
        <span class="rule"></span>
        <label class="radius">Radius
          <input #radiusField type="text" inputmode="decimal" [value]="radiusValue()" aria-label="Radius in kilometres" (change)="onRadius(radiusField)"> km
        </label>
      }
      <span class="rule"></span>

      <span class="hint">{{ measure() ? measure() + ' · ' : '' }}{{ hint() }}</span>

      <button type="button" class="btn btn-ghost btn-sm" (click)="cancel.emit()">Cancel</button>
      <button type="button" class="btn btn-primary btn-sm" [disabled]="!check().ok" [title]="check().ok ? '' : reason()" (click)="finish.emit()">Finish</button>
    </div>
  `,
  styles: [`
    :host { display: block; pointer-events: none; }
    .toolbar {
      pointer-events: auto;
      display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; padding: 8px 10px 8px 12px; max-width: 100%;
      border-radius: var(--radius-lg); background: var(--bg-panel); backdrop-filter: blur(16px);
      border: 1px solid var(--border-color); box-shadow: var(--shadow-lg); white-space: nowrap;
    }
    .eyebrow { font-family: var(--font-mono); font-size: 0.65rem; font-weight: 600; letter-spacing: 0.12em; text-transform: uppercase; color: var(--text-muted); }
    .rule { width: 1px; height: 24px; background: var(--border-color); }
    .shapes { display: flex; gap: 2px; padding: 2px; border-radius: var(--radius-md); background: var(--bg-primary); border: 1px solid var(--border-color); }
    .shape {
      display: inline-flex; align-items: center; gap: 6px; height: 30px; padding: 0 10px; border-radius: 6px;
      background: transparent; color: var(--text-secondary); font-size: 0.8rem; font-weight: 500;
      &:hover { color: var(--text-primary); }
      &.active { background: color-mix(in srgb, var(--accent-blue) 20%, transparent); color: var(--accent-blue); }
    }
    .colours { display: flex; align-items: center; gap: 6px; }
    .swatch {
      width: 18px; height: 18px; border-radius: 50%; border: 2px solid transparent; padding: 0;
      &.active { border-color: #fff; box-shadow: 0 0 0 2px var(--swatch); }
    }
    .hex, .radius {
      display: inline-flex; align-items: center; gap: 4px; height: 26px; padding: 0 6px; border-radius: 6px;
      background: var(--bg-primary); border: 1px solid var(--border-color);
      font-family: var(--font-mono); font-size: 0.7rem; color: var(--text-secondary);
      input { width: 52px; background: transparent; border: none; outline: none; padding: 0; color: var(--text-primary); font-family: inherit; font-size: inherit; box-shadow: none; }
    }
    .radius input { width: 44px; text-align: right; }
    .chip { width: 12px; height: 12px; border-radius: 3px; }
    .hint { font-size: 0.7rem; color: var(--text-muted); }
  `],
})
export class GeofenceToolbarComponent {
  session = input.required<DrawSession>();

  shapeChange = output<GeofenceShape>();
  colorChange = output<string>();
  radiusChange = output<number>();
  finish = output<void>();
  cancel = output<void>();

  protected readonly shapes = SHAPES;
  protected readonly swatches = GEOFENCE_COLORS.map((hex, i) => ({ hex, name: SWATCH_NAMES[i] }));

  protected hexValue = computed(() => this.session().color.replace('#', ''));
  protected radiusValue = computed(() => (this.session().radiusM / 1000).toFixed(1));
  protected hint = computed(() => SHAPES.find((s) => s.shape === this.session().shape)?.hint ?? '');
  protected measure = computed(() => liveMeasure(this.session()));
  protected check = computed(() => canFinish(this.session()));
  protected reason = computed(() => { const c = this.check(); return c.ok ? '' : c.reason; });

  protected onHex(field: HTMLInputElement): void {
    const hex = normalizeHex(field.value);
    if (!hex) {
      field.value = this.hexValue();
      return;
    }
    this.colorChange.emit(hex);
  }

  protected onRadius(field: HTMLInputElement): void {
    const km = Number.parseFloat(field.value.replace(',', '.'));
    if (!Number.isFinite(km) || km <= 0) {
      field.value = this.radiusValue();
      return;
    }
    this.radiusChange.emit(Math.round(km * 1000));
  }

  @HostListener('document:keydown', ['$event'])
  onKeydown(event: KeyboardEvent): void {
    const target = event.target as HTMLElement | null;
    const tag = target?.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
    if (event.key === 'Escape') {
      event.preventDefault();
      this.cancel.emit();
    } else if (event.key === 'Enter' && this.check().ok) {
      event.preventDefault();
      this.finish.emit();
    }
  }
}
