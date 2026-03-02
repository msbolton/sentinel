import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ThemePickerComponent } from './theme-picker.component';
import { ThemeService, ThemePreset } from '../../core/services/theme.service';

describe('ThemePickerComponent', () => {
  let component: ThemePickerComponent;
  let fixture: ComponentFixture<ThemePickerComponent>;
  let themeService: ThemeService;

  beforeEach(async () => {
    localStorage.clear();
    document.body.removeAttribute('data-theme');

    await TestBed.configureTestingModule({
      imports: [ThemePickerComponent],
    }).compileComponents();

    themeService = TestBed.inject(ThemeService);
    fixture = TestBed.createComponent(ThemePickerComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  afterEach(() => {
    localStorage.clear();
    document.body.removeAttribute('data-theme');
    TestBed.resetTestingModule();
  });

  it('should render collapsed pill with "STYLE PRESETS" label', () => {
    const pill = fixture.nativeElement.querySelector('.pill-header');
    expect(pill).toBeTruthy();
    expect(pill.querySelector('.pill-label').textContent.trim()).toBe('STYLE PRESETS');

    const options = fixture.nativeElement.querySelectorAll('.theme-option');
    expect(options.length).toBe(0);
  });

  it('should show theme options when pill is clicked', () => {
    const pill = fixture.nativeElement.querySelector('.pill-header');
    pill.click();
    fixture.detectChanges();

    const options = fixture.nativeElement.querySelectorAll('.theme-option');
    expect(options.length).toBe(4);

    const labels = Array.from(options).map(
      (btn: any) => btn.querySelector('.theme-name').textContent.trim(),
    );
    expect(labels).toEqual(['Normal', 'CRT', 'Night Vision', 'FLIR']);
  });

  it('should call ThemeService.setTheme when an option is clicked', () => {
    const spy = jest.spyOn(themeService, 'setTheme');

    component.expanded.set(true);
    fixture.detectChanges();

    const buttons = fixture.nativeElement.querySelectorAll('.theme-option');
    buttons[1].click();

    expect(spy).toHaveBeenCalledWith(ThemePreset.CRT);
  });

  it('should show checkmark on the active theme', () => {
    component.expanded.set(true);
    fixture.detectChanges();

    let checks = fixture.nativeElement.querySelectorAll('.swatch-check');
    expect(checks.length).toBe(1);

    const activeButton = fixture.nativeElement.querySelector('.theme-option.active');
    expect(activeButton.querySelector('.theme-name').textContent.trim()).toBe('Normal');

    themeService.setTheme(ThemePreset.FLIR);
    fixture.detectChanges();

    checks = fixture.nativeElement.querySelectorAll('.swatch-check');
    expect(checks.length).toBe(1);

    const newActive = fixture.nativeElement.querySelector('.theme-option.active');
    expect(newActive.querySelector('.theme-name').textContent.trim()).toBe('FLIR');
  });

  it('should collapse when clicking outside the component', () => {
    component.expanded.set(true);
    fixture.detectChanges();

    expect(component.expanded()).toBe(true);

    document.body.click();

    expect(component.expanded()).toBe(false);
  });

  it('should not collapse when clicking inside the component', () => {
    component.expanded.set(true);
    fixture.detectChanges();

    fixture.nativeElement.querySelector('.pill-container').click();

    expect(component.expanded()).toBe(true);
  });

  it('should rotate the plus icon when expanded', () => {
    const icon = fixture.nativeElement.querySelector('.pill-icon');
    expect(icon.classList.contains('rotated')).toBe(false);

    component.expanded.set(true);
    fixture.detectChanges();

    const iconAfter = fixture.nativeElement.querySelector('.pill-icon');
    expect(iconAfter.classList.contains('rotated')).toBe(true);
  });
});
