import { TestBed } from '@angular/core/testing';
import { ThemeService, ThemePreset } from './theme.service';

describe('ThemeService', () => {
  let service: ThemeService;

  beforeEach(() => {
    localStorage.clear();
    document.body.removeAttribute('data-theme');
    TestBed.configureTestingModule({});
  });

  afterEach(() => {
    localStorage.clear();
    document.body.removeAttribute('data-theme');
    TestBed.resetTestingModule();
  });

  function createService(): ThemeService {
    return TestBed.inject(ThemeService);
  }

  it('should default to NORMAL when localStorage is empty', () => {
    service = createService();
    expect(service.currentTheme()).toBe(ThemePreset.NORMAL);
    expect(document.body.getAttribute('data-theme')).toBe('normal');
  });

  it('should set data-theme on body and save to localStorage', () => {
    service = createService();
    service.setTheme(ThemePreset.CRT);
    expect(document.body.getAttribute('data-theme')).toBe('crt');
    expect(localStorage.getItem('sentinel-theme')).toBe('crt');
  });

  it('should restore saved theme from localStorage on init', () => {
    localStorage.setItem('sentinel-theme', 'night-vision');
    service = createService();
    expect(service.currentTheme()).toBe(ThemePreset.NIGHT_VISION);
    expect(document.body.getAttribute('data-theme')).toBe('night-vision');
  });

  it('should fall back to NORMAL for invalid localStorage value', () => {
    localStorage.setItem('sentinel-theme', 'invalid-theme');
    service = createService();
    expect(service.currentTheme()).toBe(ThemePreset.NORMAL);
    expect(document.body.getAttribute('data-theme')).toBe('normal');
  });

  it('should emit on activeTheme$ when theme changes', (done) => {
    service = createService();
    const emitted: ThemePreset[] = [];
    service.activeTheme$.subscribe((theme) => {
      emitted.push(theme);
      if (emitted.length === 2) {
        expect(emitted).toEqual([ThemePreset.NORMAL, ThemePreset.FLIR]);
        done();
      }
    });
    service.setTheme(ThemePreset.FLIR);
  });

  it('should reflect current value in currentTheme signal', () => {
    service = createService();
    expect(service.currentTheme()).toBe(ThemePreset.NORMAL);
    service.setTheme(ThemePreset.CRT);
    expect(service.currentTheme()).toBe(ThemePreset.CRT);
  });
});
