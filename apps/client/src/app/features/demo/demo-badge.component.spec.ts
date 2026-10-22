import { TestBed } from '@angular/core/testing';
import { BehaviorSubject } from 'rxjs';
import { DemoBadgeComponent } from './demo-badge.component';
import { AuthService, UserProfile } from '../../core/services/auth.service';

describe('DemoBadgeComponent', () => {
  let profile: BehaviorSubject<UserProfile | null>;
  let guest: boolean;

  function render(): HTMLElement {
    const fixture = TestBed.createComponent(DemoBadgeComponent);
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  }

  beforeEach(() => {
    guest = false;
    profile = new BehaviorSubject<UserProfile | null>(null);
    TestBed.configureTestingModule({
      imports: [DemoBadgeComponent],
      providers: [{ provide: AuthService, useValue: { isGuest: () => guest, userProfile$: profile } }],
    });
  });

  it('badge renders only for guests', () => {
    const el = render();
    expect(el.textContent).not.toContain('Demo');

    const fixture = TestBed.createComponent(DemoBadgeComponent);
    guest = true;
    profile.next({ username: 'guest-1a2b3c4d', roles: ['sentinel-guest'], classificationLevel: 'UNCLASSIFIED' } as UserProfile);
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).toContain('Demo');
  });

  it('follows the guest signing out', () => {
    guest = true;
    const fixture = TestBed.createComponent(DemoBadgeComponent);
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).toContain('Demo');

    guest = false;
    profile.next(null);
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).not.toContain('Demo');
  });

  it('collapsed rail: renders the compact dot-only variant, keeping the words for screen readers', () => {
    guest = true;
    const fixture = TestBed.createComponent(DemoBadgeComponent);
    fixture.componentRef.setInput('compact', true);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    const pill = el.querySelector('.badge-demo') as HTMLElement;
    expect(pill.classList.contains('compact')).toBe(true);
    expect(el.querySelector('.dot')).not.toBeNull();
    // The visible label is hidden but still read out.
    const label = Array.from(el.querySelectorAll('span')).find((s) => s.textContent === 'Demo') as HTMLElement;
    expect(label.classList.contains('sr-only')).toBe(true);
    expect(pill.getAttribute('title')).toContain('read-only demo');

    fixture.componentRef.setInput('compact', false);
    fixture.detectChanges();
    expect(pill.classList.contains('compact')).toBe(false);
    expect(label.classList.contains('sr-only')).toBe(false);
  });
});
