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
  description: string;
  swatch: string;
}

@Component({
  selector: 'app-theme-picker',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="theme-card">
      <div class="theme-card-header">
        <div class="theme-card-icon">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="13.5" cy="6.5" r="2.5"/>
            <circle cx="19" cy="13" r="2"/>
            <circle cx="16" cy="19" r="2"/>
            <circle cx="8" cy="19" r="2"/>
            <circle cx="5" cy="13" r="2"/>
            <path d="M12 2a10 10 0 0 0 0 20 2 2 0 0 0 2-2v-1a2 2 0 0 1 2-2h1a2 2 0 0 0 2-2 10 10 0 0 0-7-13z"/>
          </svg>
        </div>
        <div class="theme-card-title">
          <span class="title-label">Display Theme</span>
          <span class="title-value">{{ activeLabel() }}</span>
        </div>
      </div>
      <div class="theme-options">
        @for (option of themeOptions; track option.value) {
          <button
            class="theme-option"
            [class.active]="currentTheme() === option.value"
            (click)="selectTheme(option.value)">
            <span class="theme-swatch" [style.background]="option.swatch">
              @if (currentTheme() === option.value) {
                <svg class="swatch-check" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="20 6 9 17 4 12"/>
                </svg>
              }
            </span>
            <div class="theme-info">
              <span class="theme-name">{{ option.label }}</span>
              <span class="theme-desc">{{ option.description }}</span>
            </div>
          </button>
        }
      </div>
    </div>
  `,
  styles: [`
    :host {
      display: block;
    }

    .theme-card {
      width: 260px;
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      border-radius: var(--radius-lg);
      box-shadow: var(--shadow-lg);
      overflow: hidden;
      animation: floatIn 250ms cubic-bezier(0.16, 1, 0.3, 1);
    }

    .theme-card-header {
      display: flex;
      align-items: center;
      gap: 12px;
      padding: 16px;
      border-bottom: 1px solid var(--border-color);
    }

    .theme-card-icon {
      width: 40px;
      height: 40px;
      border-radius: var(--radius-md);
      background: color-mix(in srgb, var(--accent-blue) 12%, transparent);
      border: 1px solid color-mix(in srgb, var(--accent-blue) 20%, transparent);
      display: flex;
      align-items: center;
      justify-content: center;
      color: var(--accent-blue);
      flex-shrink: 0;
    }

    .theme-card-title {
      display: flex;
      flex-direction: column;
      gap: 2px;
    }

    .title-label {
      font-size: 0.7rem;
      font-weight: 500;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: var(--text-muted);
      font-family: var(--font-mono);
    }

    .title-value {
      font-size: 1rem;
      font-weight: 600;
      color: var(--text-primary);
    }

    .theme-options {
      padding: 8px;
      display: flex;
      flex-direction: column;
      gap: 4px;
    }

    .theme-option {
      width: 100%;
      display: flex;
      align-items: center;
      gap: 12px;
      padding: 10px 12px;
      background: transparent;
      color: var(--text-secondary);
      border-radius: var(--radius-md);
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
      width: 32px;
      height: 32px;
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

    .theme-info {
      display: flex;
      flex-direction: column;
      gap: 1px;
      text-align: left;
    }

    .theme-name {
      font-size: 0.85rem;
      font-weight: 500;
    }

    .theme-desc {
      font-size: 0.7rem;
      color: var(--text-muted);
      font-family: var(--font-mono);
    }

    @keyframes floatIn {
      from {
        opacity: 0;
        transform: translateY(-8px) scale(0.97);
      }
      to {
        opacity: 1;
        transform: translateY(0) scale(1);
      }
    }
  `],
})
export class ThemePickerComponent {
  readonly closed = output<void>();

  private readonly themeService = inject(ThemeService);
  private readonly elementRef = inject(ElementRef);

  readonly currentTheme = this.themeService.currentTheme;

  readonly themeOptions: ThemeOption[] = [
    { value: ThemePreset.NORMAL,       label: 'Normal',       description: 'Default dark',   swatch: '#0a0e17' },
    { value: ThemePreset.CRT,          label: 'CRT',          description: 'Retro terminal', swatch: '#001a00' },
    { value: ThemePreset.NIGHT_VISION, label: 'Night Vision',  description: 'Low-light ops',  swatch: '#001000' },
    { value: ThemePreset.FLIR,         label: 'FLIR',         description: 'Thermal view',   swatch: '#0a0a1a' },
  ];

  activeLabel(): string {
    const active = this.themeOptions.find((o) => o.value === this.currentTheme());
    return active?.label ?? 'Normal';
  }

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
