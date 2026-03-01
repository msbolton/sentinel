import { Test, TestingModule } from '@nestjs/testing';
import { BadRequestException } from '@nestjs/common';
import { SearchController } from './search.controller';
import { OpenSearchService } from './opensearch.service';

describe('SearchController', () => {
  let controller: SearchController;
  let openSearchService: {
    search: jest.Mock;
    searchNearby: jest.Mock;
    suggest: jest.Mock;
    getFacets: jest.Mock;
  };

  beforeEach(async () => {
    openSearchService = {
      search: jest.fn(),
      searchNearby: jest.fn(),
      suggest: jest.fn(),
      getFacets: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      controllers: [SearchController],
      providers: [
        { provide: OpenSearchService, useValue: openSearchService },
      ],
    }).compile();

    controller = module.get<SearchController>(SearchController);
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  // ─── GET /search ──────────────────────────────────────────────────────

  describe('search', () => {
    it('should call openSearchService.search with a SearchQuery built from query params', async () => {
      const mockResult = {
        total: 1,
        page: 1,
        pageSize: 20,
        hits: [{ id: '1', name: 'Test Entity', entityType: 'VESSEL' }],
        facets: { entityTypes: {}, sources: {}, classifications: {} },
      };
      openSearchService.search.mockResolvedValue(mockResult);

      const dto = {
        q: 'test',
        page: 1,
        pageSize: 20,
        types: ['VESSEL'],
        sources: ['AIS'],
        classifications: ['UNCLASSIFIED'],
        north: 40,
        south: 38,
        east: -76,
        west: -78,
      };

      const result = await controller.search(dto as any);

      expect(openSearchService.search).toHaveBeenCalledWith({
        q: 'test',
        page: 1,
        pageSize: 20,
        types: ['VESSEL'],
        sources: ['AIS'],
        classifications: ['UNCLASSIFIED'],
        north: 40,
        south: 38,
        east: -76,
        west: -78,
      });
      expect(result).toEqual(mockResult);
    });

    it('should omit bounding box fields when they are not all present', async () => {
      openSearchService.search.mockResolvedValue({ total: 0, page: 1, pageSize: 20, hits: [] });

      const dto = { q: 'test', north: 40 }; // missing south, east, west

      await controller.search(dto as any);

      const calledWith = openSearchService.search.mock.calls[0][0];
      expect(calledWith.north).toBeUndefined();
      expect(calledWith.south).toBeUndefined();
      expect(calledWith.east).toBeUndefined();
      expect(calledWith.west).toBeUndefined();
    });
  });

  // ─── GET /search/nearby ───────────────────────────────────────────────

  describe('searchNearby', () => {
    it('should call openSearchService.searchNearby with a NearbyQuery built from query params', async () => {
      const mockResult = {
        total: 2,
        page: 1,
        pageSize: 20,
        hits: [{ id: '2', name: 'Nearby Entity', entityType: 'AIRCRAFT' }],
      };
      openSearchService.searchNearby.mockResolvedValue(mockResult);

      const dto = {
        lat: 38.9,
        lng: -77.0,
        radiusKm: 50,
        q: 'aircraft',
        page: 1,
        pageSize: 10,
      };

      const result = await controller.searchNearby(dto as any);

      expect(openSearchService.searchNearby).toHaveBeenCalledWith({
        lat: 38.9,
        lng: -77.0,
        radiusKm: 50,
        q: 'aircraft',
        page: 1,
        pageSize: 10,
      });
      expect(result).toEqual(mockResult);
    });
  });

  // ─── GET /search/suggest ──────────────────────────────────────────────

  describe('suggest', () => {
    it('should throw BadRequestException when query is shorter than 2 characters', async () => {
      await expect(controller.suggest({ q: 'a' } as any)).rejects.toThrow(
        BadRequestException,
      );
      expect(openSearchService.suggest).not.toHaveBeenCalled();
    });

    it('should throw BadRequestException when query is empty', async () => {
      await expect(controller.suggest({ q: '' } as any)).rejects.toThrow(
        BadRequestException,
      );
      expect(openSearchService.suggest).not.toHaveBeenCalled();
    });

    it('should throw BadRequestException when query is only whitespace', async () => {
      await expect(controller.suggest({ q: ' ' } as any)).rejects.toThrow(
        BadRequestException,
      );
      expect(openSearchService.suggest).not.toHaveBeenCalled();
    });

    it('should call openSearchService.suggest for a valid query (>= 2 chars)', async () => {
      const mockSuggestions = [
        { id: '1', name: 'Test Vessel', entityType: 'VESSEL' },
        { id: '2', name: 'Test Aircraft', entityType: 'AIRCRAFT' },
      ];
      openSearchService.suggest.mockResolvedValue(mockSuggestions);

      const result = await controller.suggest({ q: 'te' } as any);

      expect(openSearchService.suggest).toHaveBeenCalledWith('te');
      expect(result).toEqual(mockSuggestions);
    });
  });

  // ─── GET /search/facets ───────────────────────────────────────────────

  describe('facets', () => {
    it('should call openSearchService.getFacets', async () => {
      const mockFacets = {
        entityTypes: { VESSEL: 10, AIRCRAFT: 5 },
        sources: { AIS: 8, RADAR: 7 },
        classifications: { UNCLASSIFIED: 15 },
      };
      openSearchService.getFacets.mockResolvedValue(mockFacets);

      const result = await controller.facets();

      expect(openSearchService.getFacets).toHaveBeenCalled();
      expect(result).toEqual(mockFacets);
    });
  });
});
