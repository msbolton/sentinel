import {
  Component,
  ChangeDetectionStrategy,
  output,
  inject,
  HostListener,
  ElementRef,
} from '@angular/core';
import { ThemeService, ThemePreset } from '../../core/services/theme.service';

interface ThemeOption {
  value: ThemePreset;
  label: string;
  swatch: string;
}

@Component({
  selector: 'app-theme-picker',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="theme-panel">
      <div class="theme-panel-header">
        <span class="theme-panel-title">Display Theme</span>
        <button class="theme-panel-close" (click)="closed.emit()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 6L6 18"/><path d="M6 6l12 12"/>
          </svg>
        </button>
      </div>
      <div class="theme-options">
        @for (option of themeOptions; track option.value) {
          <button
            class="theme-option"
            [class.active]="currentTheme() === option.value"
            (click)="selectTheme(option.value)">
            <span class="theme-swatch" [style.background]="option.swatch"></span>
            <span class="theme-name">{{ option.label }}</span>
            @if (currentTheme() === option.value) {
              <svg class="theme-check" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="20 6 9 17 4 12"/>
              </svg>
            }
          </button>
        }
      </div>
    </div>
  `,
  styles: [`
    :host {
      display: block;
      position: fixed;
      top: 0;
      left: var(--sidebar-width);
      height: calc(100% - var(--status-bar-height));
      z-index: 950;
    }

    .theme-panel {
      width: 220px;
      height: 100%;
      background: var(--bg-secondary);
      border-right: 1px solid var(--border-color);
      display: flex;
      flex-direction: column;
      animation: slideIn 200ms ease;
    }

    .theme-panel-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 14px 16px;
      border-bottom: 1px solid var(--border-color);
    }

    .theme-panel-title {
      font-size: 0.8rem;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      color: var(--text-secondary);
      font-family: var(--font-mono);
    }

    .theme-panel-close {
      background: transparent;
      color: var(--text-muted);
      padding: 4px;
      border-radius: var(--radius-sm);
      display: flex;
      align-items: center;
      justify-content: center;
      transition: all var(--transition-fast);

      &:hover {
        color: var(--text-primary);
        background: color-mix(in srgb, var(--text-muted) 15%, transparent);
      }
    }

    .theme-options {
      padding: 8px 0;
    }

    .theme-option {
      width: 100%;
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 10px 16px;
      background: transparent;
      color: var(--text-secondary);
      font-size: 0.85rem;
      transition: all var(--transition-fast);

      &:hover {
        background: color-mix(in srgb, var(--text-muted) 10%, transparent);
        color: var(--text-primary);
      }

      &.active {
        color: var(--accent-blue);
        background: color-mix(in srgb, var(--accent-blue) 10%, transparent);
      }
    }

    .theme-swatch {
      width: 20px;
      height: 20px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border-color);
      flex-shrink: 0;
    }

    .theme-name {
      flex: 1;
      text-align: left;
    }

    .theme-check {
      flex-shrink: 0;
      color: var(--accent-blue);
    }

    @keyframes slideIn {
      from { transform: translateX(-100%); opacity: 0; }
      to { transform: translateX(0); opacity: 1; }
    }
  `],
})
export class ThemePickerComponent {
  readonly closed = output<void>();

  private readonly themeService = inject(ThemeService);
  private readonly elementRef = inject(ElementRef);

  readonly currentTheme = this.themeService.currentTheme;

  readonly themeOptions: ThemeOption[] = [
    { value: ThemePreset.NORMAL,       label: 'Normal',       swatch: '#0a0e17' },
    { value: ThemePreset.CRT,          label: 'CRT',          swatch: '#001a00' },
    { value: ThemePreset.NIGHT_VISION, label: 'Night Vision',  swatch: '#001000' },
    { value: ThemePreset.FLIR,         label: 'FLIR',         swatch: '#0a0a1a' },
  ];

  selectTheme(preset: ThemePreset): void {
    this.themeService.setTheme(preset);
  }

  @HostListener('document:click', ['$event'])
  onDocumentClick(event: MouseEvent): void {
    if (!this.elementRef.nativeElement.contains(event.target)) {
      this.closed.emit();
    }
  }
}
