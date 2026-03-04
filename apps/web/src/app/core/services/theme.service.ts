import { Injectable } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { BehaviorSubject } from 'rxjs';

export enum ThemePreset {
  NORMAL = 'normal',
  CRT = 'crt',
  NIGHT_VISION = 'night-vision',
  FLIR = 'flir',
  FLIR_IRON_BOW = 'flir-iron-bow',
}

const STORAGE_KEY = 'sentinel-theme';

const validThemes = new Set<string>(Object.values(ThemePreset));

@Injectable({ providedIn: 'root' })
export class ThemeService {
  private readonly themeSubject: BehaviorSubject<ThemePreset>;

  readonly activeTheme$;
  readonly currentTheme;

  constructor() {
    const saved = localStorage.getItem(STORAGE_KEY);
    const initial = saved && validThemes.has(saved)
      ? (saved as ThemePreset)
      : ThemePreset.NORMAL;

    this.themeSubject = new BehaviorSubject<ThemePreset>(initial);
    this.activeTheme$ = this.themeSubject.asObservable();
    this.currentTheme = toSignal(this.activeTheme$, { initialValue: initial });

    this.applyTheme(initial);
  }

  setTheme(preset: ThemePreset): void {
    this.applyTheme(preset);
    localStorage.setItem(STORAGE_KEY, preset);
    this.themeSubject.next(preset);
  }

  private applyTheme(preset: ThemePreset): void {
    document.body.setAttribute('data-theme', preset);
  }
}
