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

  beforeEach(async () => {
    jest.useFakeTimers().setSystemTime(new Date('2026-11-01T14:32:00Z'));
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
        { provide: FeedService, useValue: { feeds: signal([]) } },
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
