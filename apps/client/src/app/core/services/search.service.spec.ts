import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { SearchService } from './search.service';
import { Classification, EntitySource, EntityType } from '../../shared/models/entity.model';

describe('SearchService', () => {
  let service: SearchService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    service = TestBed.inject(SearchService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('sends only the query when no filter is set', () => {
    service.search({ q: 'ual' }).subscribe();
    const req = http.expectOne((r) => r.url === '/api/v1/search');
    expect(req.request.params.keys()).toEqual(['q']);
    expect(req.request.params.get('q')).toBe('ual');
    req.flush({ total: 0, page: 1, pageSize: 20, hits: [] });
  });

  it('encodes filters, the bounding box and paging with the server names', () => {
    service
      .search({
        q: 'ual',
        types: [EntityType.AIRCRAFT, EntityType.VESSEL],
        sources: [EntitySource.AIS],
        classifications: [Classification.UNCLASSIFIED],
        bounds: { north: 50, south: 40, east: -70, west: -80 },
        page: 2,
        pageSize: 50,
      })
      .subscribe();
    const p = http.expectOne((r) => r.url === '/api/v1/search').request.params;
    expect(p.get('types')).toBe('AIRCRAFT,VESSEL');
    expect(p.get('sources')).toBe('AIS');
    expect(p.get('classifications')).toBe('UNCLASSIFIED');
    expect([p.get('north'), p.get('south'), p.get('east'), p.get('west')]).toEqual(['50', '40', '-70', '-80']);
    expect(p.get('page')).toBe('2');
    expect(p.get('pageSize')).toBe('50');
  });
});
