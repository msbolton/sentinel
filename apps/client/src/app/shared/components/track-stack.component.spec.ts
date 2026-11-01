import { readFileSync } from 'fs';
import { join } from 'path';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { BehaviorSubject } from 'rxjs';
import { TrackStackComponent } from './track-stack.component';
import { EntityService } from '../../core/services/entity.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { SelectionService } from '../../core/services/selection.service';
import { Affiliation, Classification, Entity, EntitySource, EntityType } from '../models/entity.model';

const NOW = Date.parse('2026-11-01T14:32:00Z');
const seenAgo = (s: number) => new Date(NOW - s * 1000).toISOString();

function track(id: string, over: Partial<Entity> = {}): Entity {
  return {
    id,
    entityType: EntityType.VESSEL,
    name: 'mv atlas',
    source: EntitySource.OPENSKY,
    classification: Classification.UNCLASSIFIED,
    metadata: {},
    affiliations: [],
    createdAt: seenAgo(3600),
    updatedAt: seenAgo(3600),
    position: { latitude: 1, longitude: 1 },
    ...over,
  };
}

const view = { north: 10, south: -10, east: 10, west: -10 };

describe('TrackStackComponent', () => {
  let entities$: BehaviorSubject<Map<string, Entity>>;
  let viewport: MapViewportService;
  let selection: SelectionService;
  let fixture: ComponentFixture<TrackStackComponent>;

  const rows = (): HTMLButtonElement[] => Array.from(fixture.nativeElement.querySelectorAll('.row'));

  beforeEach(async () => {
    jest.useFakeTimers().setSystemTime(NOW);
    entities$ = new BehaviorSubject(new Map<string, Entity>());
    await TestBed.configureTestingModule({
      imports: [TrackStackComponent],
      providers: [{ provide: EntityService, useValue: { currentEntities$: entities$, getEntityById: () => undefined } }],
    }).compileComponents();
    viewport = TestBed.inject(MapViewportService);
    selection = TestBed.inject(SelectionService);
    fixture = TestBed.createComponent(TrackStackComponent);
    fixture.detectChanges();
  });

  afterEach(() => {
    fixture.destroy();
    jest.useRealTimers();
  });

  function load(...list: Entity[]): void {
    entities$.next(new Map(list.map((e) => [e.id, e])));
  }

  it('renders nothing before the map has reported a viewport', () => {
    load(track('trk-1', { lastSeenAt: seenAgo(5) }));
    fixture.detectChanges();
    expect(rows()).toHaveLength(0);
  });

  it('lists tracks in view freshest first as "ID NAME" and "AFF · age"', () => {
    load(
      track('trk-old', { lastSeenAt: seenAgo(300), name: 'old one', affiliation: Affiliation.FRIENDLY }),
      track('trk-12345678-extra', { lastSeenAt: seenAgo(12), affiliation: Affiliation.HOSTILE }),
      track('trk-away', { lastSeenAt: seenAgo(1), position: { latitude: 60, longitude: 1 } }),
    );
    viewport.set(view);
    fixture.detectChanges();

    const r = rows();
    expect(r).toHaveLength(2);
    expect(r[0].textContent).toContain('trk-1234 MV ATLAS');
    expect(r[0].textContent).toContain('HOS · 12 s ago');
    expect(r[0].classList).toContain('h');
    expect(r[1].textContent).toContain('trk-old OLD ONE');
    expect(r[1].textContent).toContain('FRI · 5 min ago');
    expect(r[1].classList).not.toContain('h');
  });

  it('says UNK when a track has no affiliation', () => {
    load(track('a', { lastSeenAt: seenAgo(3) }));
    viewport.set(view);
    fixture.detectChanges();
    expect(rows()[0].textContent).toContain('UNK · 3 s ago');
  });

  it('recomputes when the same Map instance is mutated and re-emitted', () => {
    const map = new Map<string, Entity>();
    entities$.next(map);
    viewport.set(view);
    fixture.detectChanges();
    expect(rows()).toHaveLength(0);

    map.set('a', track('a', { lastSeenAt: seenAgo(2) }));
    entities$.next(map);
    fixture.detectChanges();
    expect(rows()).toHaveLength(1);
  });

  it('ages the labels on a five second tick', () => {
    load(track('a', { lastSeenAt: seenAgo(2) }));
    viewport.set(view);
    fixture.detectChanges();
    expect(rows()[0].textContent).toContain('2 s ago');

    jest.advanceTimersByTime(5000);
    fixture.detectChanges();
    expect(rows()[0].textContent).toContain('7 s ago');
  });

  it('selects the track on click and marks the selected row', () => {
    const a = track('a', { lastSeenAt: seenAgo(2) });
    const b = track('b', { lastSeenAt: seenAgo(20) });
    load(a, b);
    viewport.set(view);
    fixture.detectChanges();
    const select = jest.spyOn(selection, 'select');

    rows()[1].click();
    fixture.detectChanges();

    expect(select).toHaveBeenCalledWith(b);
    expect(rows()[1].classList).toContain('on');
    expect(rows()[0].classList).not.toContain('on');
  });

  it('stops its timer when destroyed', () => {
    const before = jest.getTimerCount();
    expect(before).toBeGreaterThan(0);
    fixture.destroy();
    expect(jest.getTimerCount()).toBeLessThan(before);
  });

  it('moves clear of an open route panel', () => {
    const source = readFileSync(join(__dirname, 'track-stack.component.ts'), 'utf8');
    expect(source).toMatch(/:host\(\.panel-open\) \{\s*right: calc\(16px \+ var\(--panel-width\)\);/);
  });
});
