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
});
