import { ComponentFixture, TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { of, throwError } from 'rxjs';
import { SearchComponent, RESULTS_PAGE_SIZE } from './search.component';
import { EntityService } from '../../core/services/entity.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { SearchService } from '../../core/services/search.service';
import { SelectionService } from '../../core/services/selection.service';
import { Classification, Entity, EntitySource, EntityType } from '../../shared/models/entity.model';
import { SearchHit, SearchRequest, SearchResponse } from '../../shared/models/search.model';

function hit(id: string, over: Partial<SearchHit> = {}): SearchHit {
  return {
    id,
    name: `Track ${id}`,
    entityType: EntityType.AIRCRAFT,
    source: EntitySource.ADS_B,
    classification: Classification.UNCLASSIFIED,
    position: { lat: 41.9, lon: -87.9 },
    ...over,
  };
}

function response(hits: SearchHit[], total = hits.length): SearchResponse {
  return { total, page: 1, pageSize: RESULTS_PAGE_SIZE, hits };
}

describe('SearchComponent', () => {
  let search: jest.Mock;
  let getEntity: jest.Mock;
  let selection: { selectById: jest.Mock; select: jest.Mock; entity: ReturnType<typeof signal<Entity | null>> };
  let bounds: ReturnType<typeof signal<{ north: number; south: number; east: number; west: number } | null>>;

  async function create(): Promise<ComponentFixture<SearchComponent>> {
    await TestBed.configureTestingModule({
      imports: [SearchComponent],
      providers: [
        { provide: SearchService, useValue: { search } },
        { provide: EntityService, useValue: { getEntity } },
        { provide: SelectionService, useValue: selection },
        { provide: MapViewportService, useValue: { bounds } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(SearchComponent);
    fixture.detectChanges();
    return fixture;
  }

  const cards = (f: ComponentFixture<SearchComponent>): HTMLButtonElement[] =>
    Array.from(f.nativeElement.querySelectorAll('.entity-card'));

  /** Flushes the signal effects, the debounce and the view. */
  function settle(f: ComponentFixture<SearchComponent>): void {
    f.detectChanges();
    jest.advanceTimersByTime(300);
    f.detectChanges();
  }

  function type(f: ComponentFixture<SearchComponent>, text: string): void {
    f.componentInstance.onSearchInput(text);
    settle(f);
  }

  beforeEach(() => {
    jest.useFakeTimers();
    search = jest.fn((req: SearchRequest) => of(response([hit('a'), hit('b', { name: 'UAL123' })])));
    getEntity = jest.fn();
    selection = { selectById: jest.fn(() => false), select: jest.fn(), entity: signal<Entity | null>(null) };
    bounds = signal<{ north: number; south: number; east: number; west: number } | null>({
      north: 50,
      south: 40,
      east: -80,
      west: -90,
    });
  });

  afterEach(() => jest.useRealTimers());

  it('does not search for a query under two characters', async () => {
    const f = await create();
    type(f, 'u');
    expect(search).not.toHaveBeenCalled();
    expect(f.nativeElement.textContent).toContain('Enter a search term');
  });

  it('searches after the debounce and renders the hits as entity cards', async () => {
    const f = await create();
    type(f, 'ual');
    expect(search).toHaveBeenCalledWith({ q: 'ual', pageSize: RESULTS_PAGE_SIZE });
    expect(cards(f).map((c) => c.querySelector('.entity-name')?.textContent?.trim())).toEqual(['Track a', 'UAL123']);
    expect(f.nativeElement.textContent).toContain('2 results');
    expect(f.componentInstance.results()[0].position).toEqual({ latitude: 41.9, longitude: -87.9 });
  });

  it('sends the filters and the viewport box with the query', async () => {
    const f = await create();
    type(f, 'ual');
    search.mockClear();
    f.componentInstance.toggleEntityType(EntityType.AIRCRAFT);
    f.componentInstance.toggleSource(EntitySource.AIS);
    f.componentInstance.setClassification(Classification.SECRET);
    f.componentInstance.toggleGeoBounded();
    settle(f);
    expect(search).toHaveBeenCalledTimes(1);
    expect(search).toHaveBeenLastCalledWith({
      q: 'ual',
      types: [EntityType.AIRCRAFT],
      sources: [EntitySource.AIS],
      classifications: [Classification.SECRET],
      bounds: { north: 50, south: 40, east: -80, west: -90 },
      pageSize: RESULTS_PAGE_SIZE,
    });
    f.componentInstance.clearFilters();
    settle(f);
    expect(search).toHaveBeenLastCalledWith({ q: 'ual', pageSize: RESULTS_PAGE_SIZE });
  });

  it('shows that search is unavailable when the request fails', async () => {
    search.mockReturnValue(throwError(() => new Error('503')));
    const f = await create();
    type(f, 'ual');
    expect(f.componentInstance.loading()).toBe(false);
    expect(f.nativeElement.textContent).toContain('Search is unavailable');
    expect(f.nativeElement.textContent).not.toContain('No entities found');
  });

  it('selects a cached track on click without fetching it', async () => {
    selection.selectById.mockReturnValue(true);
    const f = await create();
    type(f, 'ual');
    cards(f)[0].click();
    expect(selection.selectById).toHaveBeenCalledWith('a');
    expect(getEntity).not.toHaveBeenCalled();
  });

  it('fetches and selects a track the map does not have', async () => {
    const live = { id: 'b', name: 'UAL123 live' } as Entity;
    getEntity.mockReturnValue(of(live));
    const f = await create();
    type(f, 'ual');
    cards(f)[1].click();
    expect(getEntity).toHaveBeenCalledWith('b');
    expect(selection.select).toHaveBeenCalledWith(live);
  });

  it('selects the indexed copy when the track is gone from the entity service', async () => {
    getEntity.mockReturnValue(throwError(() => new Error('404')));
    const f = await create();
    type(f, 'ual');
    cards(f)[0].click();
    expect(selection.select).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'a', position: { latitude: 41.9, longitude: -87.9 } }),
    );
  });
});
