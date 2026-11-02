import { readFileSync } from 'fs';
import { join } from 'path';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { BehaviorSubject, of } from 'rxjs';
import { HudReadoutsComponent } from './hud-readouts.component';
import { AlertService } from '../../core/services/alert.service';
import { AuthService, UserProfile } from '../../core/services/auth.service';
import { EntityService } from '../../core/services/entity.service';
import { FeedService } from '../../core/services/feed.service';
import { MapCursorService } from '../../core/services/map-cursor.service';
import { SelectionService } from '../../core/services/selection.service';
import { WebSocketService } from '../../core/services/websocket.service';
import { Classification, Entity, EntitySource, EntityType } from '../models/entity.model';

const base: Entity = {
  id: 'trk-1',
  entityType: EntityType.VESSEL,
  name: 'MV ATLAS',
  source: EntitySource.OPENSKY,
  classification: Classification.UNCLASSIFIED,
  metadata: {},
  affiliations: [],
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-01T00:00:00Z',
};

function entityMap(n: number): Map<string, Entity> {
  const m = new Map<string, Entity>();
  for (let i = 0; i < n; i++) m.set(`e-${i}`, { ...base, id: `e-${i}` });
  return m;
}

describe('HudReadoutsComponent', () => {
  let fixture: ComponentFixture<HudReadoutsComponent>;
  let cursor: MapCursorService;
  let selection: SelectionService;
  let poll: jest.Mock;
  let release: jest.Mock;

  beforeEach(async () => {
    jest.useFakeTimers().setSystemTime(new Date('2026-11-01T14:32:00Z'));
    release = jest.fn();
    poll = jest.fn(() => release);
    const profile: UserProfile = { username: 'w.operator', roles: ['sentinel-operator'], classificationLevel: 'SECRET' };
    await TestBed.configureTestingModule({
      imports: [HudReadoutsComponent],
      providers: [
        { provide: WebSocketService, useValue: { connectionStatus$: of('connected') } },
        { provide: EntityService, useValue: { currentEntities$: of(entityMap(1284)), getEntityById: () => undefined } },
        { provide: AlertService, useValue: { unacknowledgedCount$: of(3) } },
        {
          provide: AuthService,
          useValue: {
            userProfile$: new BehaviorSubject<UserProfile | null>(profile),
            isAuthenticated$: of(true),
            isGuest: () => false,
          },
        },
        { provide: FeedService, useValue: { feeds: signal([]), poll } },
      ],
    }).compileComponents();

    cursor = TestBed.inject(MapCursorService);
    selection = TestBed.inject(SelectionService);
    fixture = TestBed.createComponent(HudReadoutsComponent);
  });

  afterEach(() => {
    fixture.destroy();
    jest.useRealTimers();
  });

  const readout = (name: string): string =>
    (fixture.nativeElement as HTMLElement).querySelector(`[data-readout="${name}"]`)?.textContent ?? '';

  it('shows the DTG clock, counts and classification', () => {
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('011432Z NOV 26');
    expect(text).toContain('1284');
    expect(text).toContain('SECRET');
    const tr = (fixture.nativeElement as HTMLElement).querySelector('.hud-tr')!.textContent!.replace(/\s+/g, ' ');
    expect(tr).toContain('trk 1284 alr 3');
  });

  it('keeps the user menu in the flow of the top-right block', () => {
    // Styles are stripped under jest-preset-angular, so check the rules in the source:
    // the corner rule must not reach the menu's host (which also carries class "hud"),
    // and the menu's HUD host is positioned relative.
    const hud = readFileSync(join(__dirname, 'hud-readouts.component.ts'), 'utf-8');
    expect(hud).toMatch(/\n\s*div\.hud \{\s*position: absolute;/);
    expect(hud).not.toMatch(/\n\s*\.hud \{/);
    const menu = readFileSync(join(__dirname, 'user-menu.component.ts'), 'utf-8');
    expect(menu).toMatch(/:host\(\.hud\) \{[^}]*position: relative;/);
    expect((fixture.nativeElement as HTMLElement).querySelector('.hud-tr app-user-menu.hud')).not.toBeNull();
  });

  it('polls feed health every 30 s and releases the poll on destroy', () => {
    expect(poll).toHaveBeenCalledWith(30000);
    expect(release).not.toHaveBeenCalled();
    fixture.destroy();
    expect(release).toHaveBeenCalledTimes(1);
  });

  it('colours the classification word by level, like the track card banner', () => {
    fixture.detectChanges();
    const cls = (fixture.nativeElement as HTMLElement).querySelector('.hud-cls')!;
    expect(cls.textContent).toContain('SECRET');
    expect(cls.classList).toContain('SECRET');
    expect(cls.classList).not.toContain('UNCLASSIFIED');
    const source = readFileSync(join(__dirname, 'hud-readouts.component.ts'), 'utf-8');
    expect(source).toMatch(/\.hud-cls\.SECRET \{ color: var\(--accent-red\); \}/);
  });

  it('lifts the bottom readouts above the timeline strip', () => {
    const source = readFileSync(join(__dirname, 'hud-readouts.component.ts'), 'utf-8');
    expect(source).toMatch(/:host\(\.timeline-open\) \.hud-bl,\s*:host\(\.timeline-open\) \.hud-br \{\s*bottom: var\(--timeline-height, 80px\);/);
  });

  it('shows dashes when the cursor is off the globe', () => {
    cursor.setCursor({ lat: 11.59, lon: 43.15 });
    fixture.detectChanges();
    expect(readout('cursor')).toContain('11.5900° N 43.1500° E');
    expect(readout('cursor')).not.toContain('—');

    cursor.setCursor(null);
    fixture.detectChanges();
    expect(readout('cursor')).toContain('—');
  });

  it('moves the right-hand readouts clear of an open route panel', () => {
    // jest-preset-angular strips component styles, so getComputedStyle sees no rules;
    // the rule is checked in the source and the host class it keys on is applied.
    const source = readFileSync(join(__dirname, 'hud-readouts.component.ts'), 'utf-8');
    expect(source).toMatch(/:host\(\.panel-open\) \.hud-tr,\s*:host\(\.panel-open\) \.hud-br \{\s*right: var\(--panel-width\);/);

    const host = fixture.nativeElement as HTMLElement;
    host.classList.add('panel-open');
    fixture.detectChanges();
    expect(host.matches('.panel-open')).toBe(true);
    expect(host.querySelector('.hud-tr')).not.toBeNull();
    expect(host.querySelector('.hud-br')).not.toBeNull();
  });

  it('shows bearing and range from the cursor to the selected track', () => {
    selection.entity.set({ ...base, position: { latitude: 12.79, longitude: 45.03 } });
    cursor.setCursor({ lat: 11.59, lon: 43.15 });
    fixture.detectChanges();
    expect(readout('bearing')).toMatch(/057°.*132 NM/);
  });

  it('shows a dash for bearing when the selected track has no position', () => {
    selection.entity.set({ ...base, position: undefined });
    cursor.setCursor({ lat: 11.59, lon: 43.15 });
    fixture.detectChanges();
    expect(readout('bearing')).toContain('—');
  });
});
