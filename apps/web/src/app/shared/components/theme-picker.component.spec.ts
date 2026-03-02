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

  it('should render all 4 theme options', () => {
    const buttons = fixture.nativeElement.querySelectorAll('.theme-option');
    expect(buttons.length).toBe(4);

    const labels = Array.from(buttons).map(
      (btn: any) => btn.querySelector('.theme-name').textContent.trim(),
    );
    expect(labels).toEqual(['Normal', 'CRT', 'Night Vision', 'FLIR']);
  });

  it('should call ThemeService.setTheme when an option is clicked', () => {
    const spy = jest.spyOn(themeService, 'setTheme');
    const buttons = fixture.nativeElement.querySelectorAll('.theme-option');

    buttons[1].click();

    expect(spy).toHaveBeenCalledWith(ThemePreset.CRT);
  });

  it('should show checkmark on the active theme', () => {
    let checks = fixture.nativeElement.querySelectorAll('.theme-check');
    expect(checks.length).toBe(1);

    const activeButton = fixture.nativeElement.querySelector('.theme-option.active');
    expect(activeButton.querySelector('.theme-name').textContent.trim()).toBe('Normal');

    themeService.setTheme(ThemePreset.FLIR);
    fixture.detectChanges();

    checks = fixture.nativeElement.querySelectorAll('.theme-check');
    expect(checks.length).toBe(1);

    const newActive = fixture.nativeElement.querySelector('.theme-option.active');
    expect(newActive.querySelector('.theme-name').textContent.trim()).toBe('FLIR');
  });

  it('should emit closed when clicking outside the component', () => {
    let closedEmitted = false;
    component.closed.subscribe(() => (closedEmitted = true));

    document.body.click();

    expect(closedEmitted).toBe(true);
  });

  it('should not emit closed when clicking inside the component', () => {
    let closedEmitted = false;
    component.closed.subscribe(() => (closedEmitted = true));

    fixture.nativeElement.querySelector('.theme-picker').click();

    expect(closedEmitted).toBe(false);
  });
});
