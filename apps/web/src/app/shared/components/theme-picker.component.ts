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
    <div class="theme-picker">
      <div class="theme-picker-header">Display Theme</div>
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
  `,
  styles: [`
    :host {
      display: block;
      position: absolute;
      bottom: 4px;
      left: calc(100% + 8px);
      z-index: 1100;
    }

    .theme-picker {
      width: 200px;
      background: var(--bg-secondary);
      border: 1px solid var(--border-color);
      border-radius: var(--radius-md);
      box-shadow: var(--shadow-lg);
      padding: 8px 0;
      animation: fadeIn 150ms ease;
    }

    .theme-picker-header {
      padding: 6px 14px 8px;
      font-size: 0.7rem;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      color: var(--text-muted);
      font-family: var(--font-mono);
    }

    .theme-option {
      width: 100%;
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 8px 14px;
      background: transparent;
      color: var(--text-secondary);
      font-size: 0.85rem;
      transition: all var(--transition-fast);

      &:hover {
        background: rgba(255, 255, 255, 0.05);
        color: var(--text-primary);
      }

      &.active {
        color: var(--accent-blue);
      }
    }

    .theme-swatch {
      width: 16px;
      height: 16px;
      border-radius: var(--radius-sm);
      border: 1px solid rgba(255, 255, 255, 0.15);
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

    @keyframes fadeIn {
      from { opacity: 0; transform: translateX(-4px); }
      to { opacity: 1; transform: translateX(0); }
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
