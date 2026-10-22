import { TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { Router } from '@angular/router';
import { SessionEndedComponent } from './session-ended.component';
import { DemoService } from '../../core/services/demo.service';

describe('SessionEndedComponent', () => {
  let ended: ReturnType<typeof signal<boolean>>;
  let navigateByUrl: jest.Mock;

  function render() {
    const fixture = TestBed.createComponent(SessionEndedComponent);
    fixture.detectChanges();
    return fixture;
  }

  beforeEach(() => {
    ended = signal(false);
    navigateByUrl = jest.fn().mockResolvedValue(true);
    TestBed.configureTestingModule({
      imports: [SessionEndedComponent],
      providers: [
        { provide: DemoService, useValue: { sessionEnded: ended } },
        { provide: Router, useValue: { navigateByUrl } },
      ],
    });
  });

  it('session-ended shows when sessionEnded is true, and its button navigates to /demo', () => {
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('[role="dialog"]')).toBeNull();

    ended.set(true);
    fixture.detectChanges();
    const dialog = el.querySelector('[role="dialog"]') as HTMLElement;
    expect(dialog).not.toBeNull();
    expect(dialog.querySelector('h2')?.textContent).toContain('Your demo session has ended');
    expect(dialog.getAttribute('aria-labelledby')).toBe(dialog.querySelector('h2')?.id);

    const button = el.querySelector('button') as HTMLButtonElement;
    expect(button.textContent).toContain('Start a new session');
    button.click();
    expect(navigateByUrl).toHaveBeenCalledWith('/demo');
  });

  it('puts focus on the button when it appears', () => {
    const fixture = render();
    ended.set(true);
    fixture.detectChanges();
    expect(document.activeElement).toBe(fixture.nativeElement.querySelector('button'));
  });
});
