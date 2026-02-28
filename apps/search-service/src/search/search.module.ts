import { Module } from '@nestjs/common';
import { OpenSearchService } from './opensearch.service';
import { SearchIndexerService } from './search-indexer.service';
import { SearchController } from './search.controller';

@Module({
  controllers: [SearchController],
  providers: [OpenSearchService, SearchIndexerService],
  exports: [OpenSearchService],
})
export class SearchModule {}
