import {
  Component,
  ChangeDetectionStrategy,
  signal,
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
    <div class="pill-container" [class.expanded]="expanded()">
      <!-- Pill / Header bar -->
      <button class="pill-header" (click)="toggle()">
        <span class="pill-label">STYLE PRESETS</span>
        <span class="pill-rule" [class.visible]="expanded()"></span>
        <span class="pill-toggle-btn">{{ expanded() ? '−' : '+' }}</span>
      </button>

      <!-- Expanded panel -->
      @if (expanded()) {
        <div class="pill-panel">
          @for (option of themeOptions; track option.value) {
            <button
              class="theme-option"
              [class.active]="currentTheme() === option.value"
              (click)="selectTheme(option.value)">
              <span class="theme-swatch" [style.background]="option.swatch">
                @if (currentTheme() === option.value) {
                  <svg class="swatch-check" width="10" height="10" viewBox="0 0 24 24"
                       fill="none" stroke="currentColor" stroke-width="3"
                       stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="20 6 9 17 4 12"/>
                  </svg>
                }
              </span>
              <span class="theme-name">{{ option.label }}</span>
            </button>
          }
        </div>
      }
    </div>
  `,
  styles: [`
    :host {
      display: block;
    }

    .pill-container {
      display: inline-flex;
      flex-direction: column;
    }

    .pill-container.expanded {
      width: 220px;
    }

    .pill-header {
      display: inline-flex;
      align-items: center;
      gap: 10px;
      padding: 10px 18px;
      min-width: 200px;
      border-radius: 24px;
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      cursor: pointer;
      font-family: var(--font-mono);
      font-size: 0.75rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--text-secondary);
      white-space: nowrap;
      transition: color var(--transition-fast), border-color var(--transition-fast);

      &:hover {
        color: var(--text-primary);
        border-color: color-mix(in srgb, var(--border-color) 100%, var(--text-muted) 30%);
      }
    }

    .pill-container.expanded .pill-header {
      width: 100%;
      border-radius: var(--radius-lg) var(--radius-lg) 0 0;
    }

    .pill-rule {
      flex: 1;
      height: 1px;

      &.visible {
        background: var(--border-color);
      }
    }

    .pill-toggle-btn {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 20px;
      height: 20px;
      border-radius: 4px;
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid var(--border-color);
      font-size: 0.85rem;
      line-height: 1;
      flex-shrink: 0;
      color: var(--text-muted);
    }

    .pill-panel {
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      border-top: none;
      border-radius: 0 0 var(--radius-lg) var(--radius-lg);
      padding: 6px;
      animation: floatIn 200ms cubic-bezier(0.16, 1, 0.3, 1);
    }

    .theme-option {
      width: 100%;
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 8px 10px;
      background: transparent;
      color: var(--text-secondary);
      border-radius: var(--radius-sm);
      transition: all var(--transition-fast);

      &:hover {
        background: color-mix(in srgb, var(--text-muted) 8%, transparent);
        color: var(--text-primary);
      }

      &.active {
        background: color-mix(in srgb, var(--accent-blue) 10%, transparent);
        color: var(--text-primary);
      }
    }

    .theme-swatch {
      width: 24px;
      height: 24px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border-color);
      flex-shrink: 0;
      display: flex;
      align-items: center;
      justify-content: center;
    }

    .swatch-check {
      color: var(--accent-blue);
      filter: drop-shadow(0 0 4px var(--accent-blue));
    }

    .theme-name {
      font-size: 0.8rem;
      font-weight: 500;
    }

    @keyframes floatIn {
      from {
        opacity: 0;
        transform: translateY(-4px);
      }
      to {
        opacity: 1;
        transform: translateY(0);
      }
    }
  `],
})
export class ThemePickerComponent {
  private readonly themeService = inject(ThemeService);
  private readonly elementRef = inject(ElementRef);

  readonly expanded = signal(false);
  readonly currentTheme = this.themeService.currentTheme;

  readonly themeOptions: ThemeOption[] = [
    { value: ThemePreset.NORMAL,        label: 'Normal',         swatch: '#0a0e17' },
    { value: ThemePreset.CRT,           label: 'CRT',            swatch: '#001a00' },
    { value: ThemePreset.NIGHT_VISION,  label: 'Night Vision',   swatch: '#001000' },
    { value: ThemePreset.FLIR,          label: 'FLIR White Hot', swatch: '#0a0a1a' },
    { value: ThemePreset.FLIR_IRON_BOW, label: 'FLIR Iron Bow',  swatch: '#1a0a0a' },
  ];

  toggle(): void {
    this.expanded.update((v) => !v);
  }

  selectTheme(preset: ThemePreset): void {
    this.themeService.setTheme(preset);
  }

  @HostListener('document:click', ['$event'])
  onDocumentClick(event: MouseEvent): void {
    if (!this.elementRef.nativeElement.contains(event.target)) {
      this.expanded.set(false);
    }
  }
}
