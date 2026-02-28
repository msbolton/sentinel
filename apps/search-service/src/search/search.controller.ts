import {
  Controller,
  Get,
  Query,
  BadRequestException,
} from '@nestjs/common';
import { OpenSearchService, SearchQuery, NearbyQuery } from './opensearch.service';
import { SearchQueryDto, SuggestQueryDto, NearbySearchDto } from './dto/search-query.dto';

@Controller('search')
export class SearchController {
  constructor(private readonly openSearchService: OpenSearchService) {}

  /**
   * GET /search?q=text&north=&south=&east=&west=&types[]=&sources[]=&page=&pageSize=
   * Full-text search with geo bounding box filter and faceted aggregations.
   */
  @Get()
  async search(@Query() dto: SearchQueryDto) {
    const query: SearchQuery = {
      q: dto.q,
      page: dto.page,
      pageSize: dto.pageSize,
      types: dto.types,
      sources: dto.sources,
      classifications: dto.classifications,
    };

    if (
      dto.north !== undefined &&
      dto.south !== undefined &&
      dto.east !== undefined &&
      dto.west !== undefined
    ) {
      query.north = dto.north;
      query.south = dto.south;
      query.east = dto.east;
      query.west = dto.west;
    }

    return this.openSearchService.search(query);
  }

  /**
   * GET /search/nearby?lat=&lng=&radiusKm=&q=&page=&pageSize=
   * Geo-distance search combined with optional text query.
   */
  @Get('nearby')
  async searchNearby(@Query() dto: NearbySearchDto) {
    const query: NearbyQuery = {
      lat: dto.lat,
      lng: dto.lng,
      radiusKm: dto.radiusKm,
      q: dto.q,
      page: dto.page,
      pageSize: dto.pageSize,
    };

    return this.openSearchService.searchNearby(query);
  }

  /**
   * GET /search/suggest?q=prefix
   * Autocomplete/suggest endpoint.
   */
  @Get('suggest')
  async suggest(@Query() dto: SuggestQueryDto) {
    if (!dto.q || dto.q.trim().length < 2) {
      throw new BadRequestException(
        'Query must be at least 2 characters for suggestions',
      );
    }

    return this.openSearchService.suggest(dto.q);
  }

  /**
   * GET /search/facets
   * Return aggregation facet counts without documents.
   */
  @Get('facets')
  async facets() {
    return this.openSearchService.getFacets();
  }
}
