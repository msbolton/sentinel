import {
  Injectable,
  Logger,
  OnModuleInit,
  InternalServerErrorException,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { Client } from '@opensearch-project/opensearch';
import { DataSource } from 'typeorm';

export interface EntityDocument {
  id: string;
  name: string;
  description?: string;
  entityType: string;
  source?: string;
  classification?: string;
  position?: { lat: number; lon: number };
  affiliations?: string[];
  metadata?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
  lastSeenAt?: string;
}

export interface SearchQuery {
  q?: string;
  north?: number;
  south?: number;
  east?: number;
  west?: number;
  types?: string[];
  sources?: string[];
  classifications?: string[];
  page?: number;
  pageSize?: number;
}

export interface SearchResult {
  total: number;
  page: number;
  pageSize: number;
  hits: EntityDocument[];
  facets?: {
    entityTypes: Record<string, number>;
    sources: Record<string, number>;
    classifications: Record<string, number>;
  };
}

export interface NearbyQuery {
  lat: number;
  lng: number;
  radiusKm: number;
  q?: string;
  page?: number;
  pageSize?: number;
}

const INDEX_NAME = 'sentinel-entities';

@Injectable()
export class OpenSearchService implements OnModuleInit {
  private readonly logger = new Logger(OpenSearchService.name);
  private client: Client;

  constructor(
    private readonly configService: ConfigService,
    private readonly dataSource: DataSource,
  ) {
    const host = this.configService.get<string>(
      'OPENSEARCH_HOST',
      'http://localhost:9200',
    );

    this.client = new Client({
      node: host,
      ssl: {
        rejectUnauthorized: false,
      },
    });
  }

  async onModuleInit(): Promise<void> {
    await this.ensureIndex();
    await this.warmIndex();
  }

  /**
   * Create the sentinel-entities index with proper mapping if it does not exist.
   */
  async ensureIndex(): Promise<void> {
    try {
      const { body: exists } = await this.client.indices.exists({
        index: INDEX_NAME,
      });

      if (!exists) {
        await this.client.indices.create({
          index: INDEX_NAME,
          body: {
            settings: {
              number_of_shards: 3,
              number_of_replicas: 1,
              analysis: {
                analyzer: {
                  autocomplete_analyzer: {
                    type: 'custom',
                    tokenizer: 'autocomplete_tokenizer',
                    filter: ['lowercase'],
                  },
                },
                tokenizer: {
                  autocomplete_tokenizer: {
                    type: 'edge_ngram',
                    min_gram: 2,
                    max_gram: 20,
                    token_chars: ['letter', 'digit'],
                  },
                },
              },
            },
            mappings: {
              properties: {
                name: {
                  type: 'text',
                  fields: {
                    keyword: { type: 'keyword' },
                    autocomplete: {
                      type: 'text',
                      analyzer: 'autocomplete_analyzer',
                      search_analyzer: 'standard',
                    },
                  },
                },
                description: { type: 'text' },
                entityType: { type: 'keyword' },
                source: { type: 'keyword' },
                classification: { type: 'keyword' },
                position: { type: 'geo_point' },
                affiliations: { type: 'keyword' },
                metadata: { type: 'object', enabled: true },
                createdAt: { type: 'date' },
                updatedAt: { type: 'date' },
                lastSeenAt: { type: 'date' },
              },
            },
          },
        });

        this.logger.log(`Created index: ${INDEX_NAME}`);
      } else {
        this.logger.log(`Index ${INDEX_NAME} already exists`);
      }
    } catch (error) {
      this.logger.error(
        'Failed to ensure OpenSearch index',
        error instanceof Error ? error.stack : String(error),
      );
    }
  }

  /**
   * Warm the OpenSearch index from PostgreSQL on startup.
   * Seeds data is written directly to Postgres, not via Kafka,
   * so this ensures the search index has all existing entities.
   */
  async warmIndex(): Promise<void> {
    try {
      // Check current document count
      const { body: countBody } = await this.client.count({ index: INDEX_NAME });
      if (countBody.count > 0) {
        this.logger.log(`Index ${INDEX_NAME} already has ${countBody.count} documents, skipping warm`);
        return;
      }

      const rows = await this.dataSource.query(`
        SELECT
          id,
          entity_type AS "entityType",
          name,
          description,
          source,
          classification,
          ST_Y(position::geometry) AS latitude,
          ST_X(position::geometry) AS longitude,
          affiliations,
          metadata,
          created_at AS "createdAt",
          updated_at AS "updatedAt",
          last_seen_at AS "lastSeenAt"
        FROM sentinel.entities
      `);

      if (rows.length === 0) {
        this.logger.log('Index warm: no entities found in Postgres');
        return;
      }

      // Build bulk request body
      const bulkBody: object[] = [];
      for (const row of rows) {
        bulkBody.push({ index: { _index: INDEX_NAME, _id: row.id } });

        const doc: EntityDocument = {
          id: row.id,
          name: row.name,
          entityType: row.entityType,
          description: row.description ?? undefined,
          source: row.source,
          classification: row.classification,
          affiliations: Array.isArray(row.affiliations) ? row.affiliations : [],
          metadata: row.metadata ?? {},
          createdAt: row.createdAt instanceof Date ? row.createdAt.toISOString() : row.createdAt,
          updatedAt: row.updatedAt instanceof Date ? row.updatedAt.toISOString() : row.updatedAt,
          lastSeenAt: row.lastSeenAt instanceof Date ? row.lastSeenAt.toISOString() : row.lastSeenAt ?? undefined,
        };

        if (row.latitude != null && row.longitude != null) {
          doc.position = { lat: parseFloat(row.latitude), lon: parseFloat(row.longitude) };
        }

        bulkBody.push(doc);
      }

      const { body: bulkResponse } = await this.client.bulk({
        body: bulkBody,
        refresh: 'wait_for',
      });

      if (bulkResponse.errors) {
        const errorItems = bulkResponse.items.filter((item: { index?: { error?: unknown } }) => item.index?.error);
        this.logger.warn(`Index warm: ${errorItems.length} documents had errors`);
      }

      this.logger.log(`Index warm: indexed ${rows.length} entities from Postgres into OpenSearch`);
    } catch (error) {
      this.logger.warn(
        `Index warm failed (DB or OpenSearch may not be ready): ${error instanceof Error ? error.message : String(error)}`,
      );
    }
  }

  /**
   * Index or update an entity document in OpenSearch.
   */
  async indexEntity(entity: EntityDocument): Promise<void> {
    try {
      await this.client.index({
        index: INDEX_NAME,
        id: entity.id,
        body: {
          name: entity.name,
          description: entity.description,
          entityType: entity.entityType,
          source: entity.source,
          classification: entity.classification,
          position: entity.position,
          affiliations: entity.affiliations,
          metadata: entity.metadata,
          createdAt: entity.createdAt,
          updatedAt: entity.updatedAt,
          lastSeenAt: entity.lastSeenAt,
        },
        refresh: 'wait_for',
      });

      this.logger.debug(`Indexed entity ${entity.id}: ${entity.name}`);
    } catch (error) {
      this.logger.error(
        `Failed to index entity ${entity.id}`,
        error instanceof Error ? error.stack : String(error),
      );
      throw new InternalServerErrorException('Failed to index entity');
    }
  }

  /**
   * Remove an entity from the OpenSearch index.
   */
  async deleteEntity(id: string): Promise<void> {
    try {
      await this.client.delete({
        index: INDEX_NAME,
        id,
        refresh: 'wait_for',
      });

      this.logger.debug(`Deleted entity ${id} from index`);
    } catch (error) {
      this.logger.warn(
        `Failed to delete entity ${id} from index`,
        error instanceof Error ? error.message : String(error),
      );
    }
  }

  /**
   * Full-text search with optional geo_bounding_box filter
   * and faceted aggregations on entityType, source, classification.
   */
  async search(query: SearchQuery): Promise<SearchResult> {
    const page = query.page || 1;
    const pageSize = query.pageSize || 20;
    const from = (page - 1) * pageSize;

    const must: object[] = [];
    const filter: object[] = [];

    // Full-text query
    if (query.q && query.q.trim()) {
      must.push({
        multi_match: {
          query: query.q,
          fields: ['name^3', 'name.keyword^5', 'description', 'affiliations'],
          type: 'best_fields',
          fuzziness: 'AUTO',
        },
      });
    } else {
      must.push({ match_all: {} });
    }

    // Geo bounding box filter
    if (
      query.north !== undefined &&
      query.south !== undefined &&
      query.east !== undefined &&
      query.west !== undefined
    ) {
      filter.push({
        geo_bounding_box: {
          position: {
            top_left: { lat: query.north, lon: query.west },
            bottom_right: { lat: query.south, lon: query.east },
          },
        },
      });
    }

    // Entity type filter
    if (query.types && query.types.length > 0) {
      filter.push({ terms: { entityType: query.types } });
    }

    // Source filter
    if (query.sources && query.sources.length > 0) {
      filter.push({ terms: { source: query.sources } });
    }

    // Classification filter
    if (query.classifications && query.classifications.length > 0) {
      filter.push({ terms: { classification: query.classifications } });
    }

    try {
      const { body } = await this.client.search({
        index: INDEX_NAME,
        body: {
          from,
          size: pageSize,
          query: {
            bool: {
              must,
              filter,
            },
          },
          aggregations: {
            entityTypes: { terms: { field: 'entityType', size: 50 } },
            sources: { terms: { field: 'source', size: 50 } },
            classifications: { terms: { field: 'classification', size: 50 } },
          },
        },
      });

      const hits = body.hits.hits.map((hit: { _id: string; _source: EntityDocument }) => ({
        ...hit._source,
        id: hit._id,
      }));

      const entityTypes: Record<string, number> = {};
      for (const bucket of body.aggregations.entityTypes.buckets) {
        entityTypes[bucket.key] = bucket.doc_count;
      }

      const sources: Record<string, number> = {};
      for (const bucket of body.aggregations.sources.buckets) {
        sources[bucket.key] = bucket.doc_count;
      }

      const classifications: Record<string, number> = {};
      for (const bucket of body.aggregations.classifications.buckets) {
        classifications[bucket.key] = bucket.doc_count;
      }

      return {
        total:
          typeof body.hits.total === 'number'
            ? body.hits.total
            : body.hits.total.value,
        page,
        pageSize,
        hits,
        facets: { entityTypes, sources, classifications },
      };
    } catch (error) {
      this.logger.error(
        'Search query failed',
        error instanceof Error ? error.stack : String(error),
      );
      throw new InternalServerErrorException('Search query failed');
    }
  }

  /**
   * Geo-distance search combined with optional text query.
   */
  async searchNearby(query: NearbyQuery): Promise<SearchResult> {
    const page = query.page || 1;
    const pageSize = query.pageSize || 20;
    const from = (page - 1) * pageSize;

    const must: object[] = [];
    const filter: object[] = [];

    // Text query
    if (query.q && query.q.trim()) {
      must.push({
        multi_match: {
          query: query.q,
          fields: ['name^3', 'description'],
          fuzziness: 'AUTO',
        },
      });
    } else {
      must.push({ match_all: {} });
    }

    // Geo-distance filter
    filter.push({
      geo_distance: {
        distance: `${query.radiusKm}km`,
        position: { lat: query.lat, lon: query.lng },
      },
    });

    try {
      const { body } = await this.client.search({
        index: INDEX_NAME,
        body: {
          from,
          size: pageSize,
          query: {
            bool: {
              must,
              filter,
            },
          },
          sort: [
            {
              _geo_distance: {
                position: { lat: query.lat, lon: query.lng },
                order: 'asc',
                unit: 'km',
              },
            },
          ],
        },
      });

      const hits = body.hits.hits.map((hit: { _id: string; _source: EntityDocument }) => ({
        ...hit._source,
        id: hit._id,
      }));

      return {
        total:
          typeof body.hits.total === 'number'
            ? body.hits.total
            : body.hits.total.value,
        page,
        pageSize,
        hits,
      };
    } catch (error) {
      this.logger.error(
        'Nearby search failed',
        error instanceof Error ? error.stack : String(error),
      );
      throw new InternalServerErrorException('Nearby search failed');
    }
  }

  /**
   * Autocomplete/suggest endpoint using edge_ngram analysis.
   */
  async suggest(prefix: string): Promise<{ name: string; id: string; entityType: string }[]> {
    try {
      const { body } = await this.client.search({
        index: INDEX_NAME,
        body: {
          size: 10,
          query: {
            match: {
              'name.autocomplete': {
                query: prefix,
                operator: 'and',
              },
            },
          },
          _source: ['name', 'entityType'],
        },
      });

      return body.hits.hits.map(
        (hit: { _id: string; _source: { name: string; entityType: string } }) => ({
          id: hit._id,
          name: hit._source.name,
          entityType: hit._source.entityType,
        }),
      );
    } catch (error) {
      this.logger.error(
        'Suggest query failed',
        error instanceof Error ? error.stack : String(error),
      );
      throw new InternalServerErrorException('Suggest query failed');
    }
  }

  /**
   * Get aggregation facet counts (without returning documents).
   */
  async getFacets(): Promise<{
    entityTypes: Record<string, number>;
    sources: Record<string, number>;
    classifications: Record<string, number>;
  }> {
    try {
      const { body } = await this.client.search({
        index: INDEX_NAME,
        body: {
          size: 0,
          aggregations: {
            entityTypes: { terms: { field: 'entityType', size: 100 } },
            sources: { terms: { field: 'source', size: 100 } },
            classifications: { terms: { field: 'classification', size: 100 } },
          },
        },
      });

      const entityTypes: Record<string, number> = {};
      for (const bucket of body.aggregations.entityTypes.buckets) {
        entityTypes[bucket.key] = bucket.doc_count;
      }

      const sources: Record<string, number> = {};
      for (const bucket of body.aggregations.sources.buckets) {
        sources[bucket.key] = bucket.doc_count;
      }

      const classifications: Record<string, number> = {};
      for (const bucket of body.aggregations.classifications.buckets) {
        classifications[bucket.key] = bucket.doc_count;
      }

      return { entityTypes, sources, classifications };
    } catch (error) {
      this.logger.error(
        'Facets query failed',
        error instanceof Error ? error.stack : String(error),
      );
      throw new InternalServerErrorException('Facets query failed');
    }
  }
}
