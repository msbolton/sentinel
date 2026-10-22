import { TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { BehaviorSubject } from 'rxjs';
import { WelcomeCardComponent } from './welcome-card.component';
import { DemoBounds, DemoSession } from '../../core/services/demo.service';
import { DemoService } from '../../core/services/demo.service';
import { AuthService, UserProfile } from '../../core/services/auth.service';
import { EmplacementService } from '../../core/services/emplacement.service';
import { Emplacement } from '../../shared/models/emplacement.model';
import { sensorBounds, visitorBounds } from './demo-bounds';

function site(id: string, feedId: string, lat: number, lon: number): Emplacement {
  return { id, feedId, active: true, position: { lat, lon } } as Emplacement;
}

describe('WelcomeCardComponent', () => {
  let guest: boolean;
  let profile: BehaviorSubject<UserProfile | null>;
  let demo: {
    session: ReturnType<typeof signal<DemoSession | null>>;
    sessionEnded: ReturnType<typeof signal<boolean>>;
    welcomeDismissed: ReturnType<typeof signal<boolean>>;
    welcomeShown: ReturnType<typeof signal<boolean>>;
    focusBounds: ReturnType<typeof signal<DemoBounds | null>>;
  };
  let sites: ReturnType<typeof signal<Emplacement[]>>;

  const list = [site('radar', 'sim', 10, 20), site('guest', 'visitors', -30, -40)];

  function render() {
    const fixture = TestBed.createComponent(WelcomeCardComponent);
    fixture.detectChanges();
    return fixture;
  }
  const dialog = (el: HTMLElement) => el.querySelector('[role="dialog"]') as HTMLElement | null;
  const button = (el: HTMLElement, label: string) =>
    Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes(label)) as HTMLButtonElement;

  beforeEach(() => {
    guest = true;
    profile = new BehaviorSubject<UserProfile | null>({ username: 'guest-1', roles: ['sentinel-guest'] } as UserProfile);
    demo = {
      session: signal<DemoSession | null>({ guestId: '1', expiresAt: '', visitorsFeedId: 'visitors' }),
      sessionEnded: signal(false),
      welcomeDismissed: signal(false),
      welcomeShown: signal(false),
      focusBounds: signal<DemoBounds | null>(null),
    };
    sites = signal<Emplacement[]>(list);
    TestBed.configureTestingModule({
      imports: [WelcomeCardComponent],
      providers: [
        { provide: AuthService, useValue: { isGuest: () => guest, userProfile$: profile } },
        { provide: DemoService, useValue: demo },
        { provide: EmplacementService, useValue: { emplacements: sites } },
      ],
    });
  });

  it('welcome card shows for a guest until dismissed, and stays dismissed this session', () => {
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    expect(dialog(el)).not.toBeNull();
    expect(dialog(el)?.querySelector('h2')?.textContent).toContain('Welcome to Sentinel');

    button(el, 'Explore').click();
    fixture.detectChanges();
    expect(dialog(el)).toBeNull();
    expect(demo.welcomeDismissed()).toBe(true);

    // A card made later in the same session (the map was left and reopened) stays away.
    const again = render();
    expect(dialog(again.nativeElement)).toBeNull();
  });

  it('does not show for anyone but a guest, nor once the session has ended', () => {
    guest = false;
    expect(dialog(render().nativeElement)).toBeNull();

    guest = true;
    demo.sessionEnded.set(true);
    expect(dialog(render().nativeElement)).toBeNull();
  });

  it('says it has rendered, so the map can ask for the location', async () => {
    const fixture = render();
    await fixture.whenStable();
    expect(demo.welcomeShown()).toBe(true);
  });

  it('Fly to the sensors sets focusBounds to sensorBounds', () => {
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    button(el, 'Fly to the sensors').click();
    expect(demo.focusBounds()).toEqual(sensorBounds(list, 'visitors'));
    expect(demo.focusBounds()).not.toBeNull();
    fixture.detectChanges();
    expect(demo.welcomeDismissed()).toBe(true);
  });

  it('Show everyone exploring sets focusBounds to visitorBounds', () => {
    const fixture = render();
    button(fixture.nativeElement, 'Show everyone exploring').click();
    expect(demo.focusBounds()).toEqual(visitorBounds(list, 'visitors'));
    expect(demo.focusBounds()).not.toBeNull();
  });

  it('disables a fly button while there is nothing to fly to', () => {
    sites.set([site('radar', 'sim', 10, 20)]);
    const fixture = render();
    expect(button(fixture.nativeElement, 'Fly to the sensors').disabled).toBe(false);
    expect(button(fixture.nativeElement, 'Show everyone exploring').disabled).toBe(true);
  });

  it('Escape dismisses it and focus starts on Explore', () => {
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    expect(document.activeElement).toBe(button(el, 'Explore'));
    dialog(el)!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    fixture.detectChanges();
    expect(dialog(el)).toBeNull();
    expect(demo.welcomeDismissed()).toBe(true);
  });

  it('links back to the portfolio and covers each point of the brief', () => {
    const el = render().nativeElement as HTMLElement;
    const link = el.querySelector('a') as HTMLAnchorElement;
    expect(link.textContent).toContain('Back to msbolton.dev');
    expect(link.getAttribute('href')).toBe('https://msbolton.dev');
    const text = el.textContent ?? '';
    expect(text).toContain('guest');
    expect(text).toContain('read-only');
    expect(text).toContain('Aircraft, vessels and satellites are live');
    expect(text).toContain('simulated');
    expect(text).toContain('violet');
  });
});
