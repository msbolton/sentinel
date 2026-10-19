import { ChangeDetectionStrategy, Component, input } from '@angular/core';

export const ICON_NAMES = [
  'globe', 'search', 'bell', 'activity', 'link', 'clock', 'map-pin', 'rss', 'settings',
  'chevron-left', 'chevron-right', 'chevron-down',
  'layers', 'crosshair', 'building', 'maximize', 'trash', 'x', 'refresh', 'navigation', 'check',
  'skip-back', 'play', 'pause', 'skip-forward', 'square',
  'zoom-in', 'zoom-out', 'fit', 'zap', 'share',
  'alert-octagon', 'alert-triangle', 'alert-circle', 'info', 'user', 'box', 'arrow-up',
  'hexagon', 'rectangle', 'circle-dot', 'ring', 'plus', 'radar',
] as const;

export type IconName = (typeof ICON_NAMES)[number];

/**
 * The one icon language of the client: 24-unit stroke glyphs that take the
 * current text colour, so every theme tints them. Decorative by default;
 * the control that holds one carries the accessible name.
 */
@Component({
  selector: 'app-icon',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <svg
      [attr.width]="size()"
      [attr.height]="size()"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      [attr.stroke-width]="strokeWidth()"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
      focusable="false">
      @switch (name()) {
        @case ('globe') {
          <svg:circle cx="12" cy="12" r="10"/><svg:path d="M2 12h20"/><svg:path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
        }
        @case ('search') {
          <svg:circle cx="11" cy="11" r="8"/><svg:path d="M21 21l-4.35-4.35"/>
        }
        @case ('bell') {
          <svg:path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><svg:path d="M13.73 21a2 2 0 0 1-3.46 0"/>
        }
        @case ('activity') {
          <svg:polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
        }
        @case ('link') {
          <svg:circle cx="5" cy="6" r="3"/><svg:circle cx="19" cy="6" r="3"/><svg:circle cx="12" cy="19" r="3"/><svg:path d="M7.5 8l4 8.5"/><svg:path d="M16.5 8l-4 8.5"/><svg:path d="M8 6h8"/>
        }
        @case ('clock') {
          <svg:circle cx="12" cy="12" r="10"/><svg:polyline points="12 6 12 12 16 14"/>
        }
        @case ('map-pin') {
          <svg:path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0 1 18 0z"/><svg:circle cx="12" cy="10" r="3"/>
        }
        @case ('rss') {
          <svg:path d="M4 11a9 9 0 0 1 9 9"/><svg:path d="M4 4a16 16 0 0 1 16 16"/><svg:circle cx="5" cy="19" r="1"/>
        }
        @case ('settings') {
          <svg:circle cx="12" cy="12" r="3"/><svg:path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        }
        @case ('chevron-left') {
          <svg:path d="M15 18l-6-6 6-6"/>
        }
        @case ('chevron-right') {
          <svg:path d="M9 18l6-6-6-6"/>
        }
        @case ('chevron-down') {
          <svg:path d="M6 9l6 6 6-6"/>
        }
        @case ('layers') {
          <svg:polygon points="12 2 2 7 12 12 22 7 12 2"/><svg:polyline points="2 17 12 22 22 17"/><svg:polyline points="2 12 12 17 22 12"/>
        }
        @case ('radar') {
          <svg:path d="M19.07 4.93A10 10 0 1 0 22 12"/><svg:path d="M16.24 7.76A6 6 0 1 0 18 12"/><svg:path d="M12 12l7-7"/><svg:circle cx="12" cy="12" r="2"/>
        }
        @case ('crosshair') {
          <svg:circle cx="12" cy="12" r="4"/><svg:path d="M12 2v4"/><svg:path d="M12 18v4"/><svg:path d="M2 12h4"/><svg:path d="M18 12h4"/>
        }
        @case ('building') {
          <svg:rect x="4" y="2" width="16" height="20" rx="2" ry="2"/><svg:path d="M9 22V12h6v10"/><svg:path d="M8 6h.01"/><svg:path d="M16 6h.01"/><svg:path d="M12 6h.01"/><svg:path d="M12 10h.01"/><svg:path d="M8 10h.01"/><svg:path d="M16 10h.01"/>
        }
        @case ('maximize') {
          <svg:path d="M8 3H5a2 2 0 0 0-2 2v3"/><svg:path d="M21 8V5a2 2 0 0 0-2-2h-3"/><svg:path d="M3 16v3a2 2 0 0 0 2 2h3"/><svg:path d="M16 21h3a2 2 0 0 0 2-2v-3"/>
        }
        @case ('trash') {
          <svg:polyline points="3 6 5 6 21 6"/><svg:path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><svg:path d="M10 11v6"/><svg:path d="M14 11v6"/><svg:path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
        }
        @case ('x') {
          <svg:path d="M18 6L6 18"/><svg:path d="M6 6l12 12"/>
        }
        @case ('refresh') {
          <svg:polyline points="23 4 23 10 17 10"/><svg:polyline points="1 20 1 14 7 14"/><svg:path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>
        }
        @case ('navigation') {
          <svg:polygon points="3 11 22 2 13 21 11 13 3 11"/>
        }
        @case ('check') {
          <svg:polyline points="20 6 9 17 4 12"/>
        }
        @case ('skip-back') {
          <svg:polygon points="19 20 9 12 19 4 19 20"/><svg:line x1="5" y1="19" x2="5" y2="5"/>
        }
        @case ('play') {
          <svg:polygon points="5 3 19 12 5 21 5 3"/>
        }
        @case ('pause') {
          <svg:rect x="6" y="4" width="4" height="16"/><svg:rect x="14" y="4" width="4" height="16"/>
        }
        @case ('skip-forward') {
          <svg:polygon points="5 4 15 12 5 20 5 4"/><svg:line x1="19" y1="5" x2="19" y2="19"/>
        }
        @case ('square') {
          <svg:rect x="4" y="4" width="16" height="16" rx="2"/>
        }
        @case ('zoom-in') {
          <svg:circle cx="11" cy="11" r="8"/><svg:path d="M21 21l-4.35-4.35"/><svg:path d="M11 8v6"/><svg:path d="M8 11h6"/>
        }
        @case ('zoom-out') {
          <svg:circle cx="11" cy="11" r="8"/><svg:path d="M21 21l-4.35-4.35"/><svg:path d="M8 11h6"/>
        }
        @case ('fit') {
          <svg:polyline points="15 3 21 3 21 9"/><svg:polyline points="9 21 3 21 3 15"/><svg:line x1="21" y1="3" x2="14" y2="10"/><svg:line x1="3" y1="21" x2="10" y2="14"/>
        }
        @case ('zap') {
          <svg:polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
        }
        @case ('share') {
          <svg:circle cx="18" cy="5" r="3"/><svg:circle cx="6" cy="12" r="3"/><svg:circle cx="18" cy="19" r="3"/><svg:line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/><svg:line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/>
        }
        @case ('alert-octagon') {
          <svg:polygon points="7.86 2 16.14 2 22 7.86 22 16.14 16.14 22 7.86 22 2 16.14 2 7.86 7.86 2"/><svg:line x1="12" y1="8" x2="12" y2="12"/><svg:line x1="12" y1="16" x2="12.01" y2="16"/>
        }
        @case ('alert-triangle') {
          <svg:path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><svg:line x1="12" y1="9" x2="12" y2="13"/><svg:line x1="12" y1="17" x2="12.01" y2="17"/>
        }
        @case ('alert-circle') {
          <svg:circle cx="12" cy="12" r="10"/><svg:line x1="12" y1="8" x2="12" y2="12"/><svg:line x1="12" y1="16" x2="12.01" y2="16"/>
        }
        @case ('info') {
          <svg:circle cx="12" cy="12" r="10"/><svg:line x1="12" y1="16" x2="12" y2="12"/><svg:line x1="12" y1="8" x2="12.01" y2="8"/>
        }
        @case ('user') {
          <svg:path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><svg:circle cx="12" cy="7" r="4"/>
        }
        @case ('hexagon') {
          <svg:polygon points="12 3 21 10 17 21 7 21 3 10"/><svg:circle cx="12" cy="3" r="1.5" fill="currentColor"/><svg:circle cx="21" cy="10" r="1.5" fill="currentColor"/><svg:circle cx="3" cy="10" r="1.5" fill="currentColor"/>
        }
        @case ('rectangle') {
          <svg:rect x="3" y="5" width="18" height="14" rx="1"/>
        }
        @case ('circle-dot') {
          <svg:circle cx="12" cy="12" r="9"/><svg:circle cx="12" cy="12" r="1.5" fill="currentColor"/>
        }
        @case ('ring') {
          <svg:circle cx="12" cy="12" r="9" stroke-dasharray="3 3"/><svg:path d="M12 7l4 5-4 5-4-5z"/>
        }
        @case ('plus') {
          <svg:path d="M12 5v14"/><svg:path d="M5 12h14"/>
        }
        @case ('arrow-up') {
          <svg:path d="M12 19V5"/><svg:path d="M5 12l7-7 7 7"/>
        }
        @case ('box') {
          <svg:path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><svg:polyline points="3.27 6.96 12 12.01 20.73 6.96"/><svg:line x1="12" y1="22.08" x2="12" y2="12"/>
        }
      }
    </svg>
  `,
  styles: [`
    :host {
      display: inline-flex;
      flex-shrink: 0;
      line-height: 0;
    }
  `],
})
export class IconComponent {
  name = input.required<IconName>();
  /** Rendered edge in px. 16 for controls, 20 for navigation, 28+ for empty states. */
  size = input<number>(16);
  strokeWidth = input<number>(2);
}
