import {
  Injectable,
  Inject,
  Logger,
  NotFoundException,
  OnModuleInit,
  OnModuleDestroy,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { ClientKafka } from '@nestjs/microservices';
import { DataSource } from 'typeorm';
import Redis from 'ioredis';
import { CreateEntityDto } from './dto/create-entity.dto';
import { UpdateEntityDto } from './dto/update-entity.dto';
import { QueryEntitiesDto } from './dto/query-entities.dto';

/**
 * Represents an entity as returned from the service layer.
 */
export interface EntityRecord {
  id: string;
  entityType: string;
  name: string;
  latitude: number;
  longitude: number;
  altitude?: number;
  heading?: number;
  speed?: number;
  classification: string;
  source: string;
  affiliation?: string;
  metadata?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}

/**
 * Paginated result set for entity queries.
 */
export interface PaginatedEntitiesResult {
  data: EntityRecord[];
  pagination: {
    page: number;
    pageSize: number;
    total: number;
    totalPages: number;
  };
}

/**
 * Redis key for the entity geospatial index.
 */
const ENTITY_GEO_KEY = 'sentinel:entities:geo';

/**
 * Redis key prefix for individual entity data cache.
 */
const ENTITY_CACHE_PREFIX = 'sentinel:entities:cache';

/**
 * Cache TTL in seconds.
 */
const CACHE_TTL = 300;

/**
 * Service layer for entity operations.
 *
 * Acts as the orchestration layer between the REST controller,
 * the entity-service microservice (via Kafka), and the Redis
 * geospatial cache. Entity positions are cached in Redis using
 * GEOADD for efficient bounding-box queries without hitting the
 * primary database.
 *
 * On startup, the service warms the Redis cache from PostgreSQL
 * so that seeded entities are immediately queryable.
 */
@Injectable()
export class EntitiesService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(EntitiesService.name);
  private readonly redis: Redis;

  constructor(
    @Inject('ENTITY_SERVICE') private readonly kafkaClient: ClientKafka,
    private readonly configService: ConfigService,
    private readonly dataSource: DataSource,
  ) {
    const redisHost = this.configService.get<string>('REDIS_HOST', 'localhost');
    const redisPort = this.configService.get<number>('REDIS_PORT', 6379);
    const redisPassword = this.configService.get<string>('REDIS_PASSWORD', '');

    this.redis = new Redis({
      host: redisHost,
      port: redisPort,
      password: redisPassword || undefined,
      retryStrategy: (times: number) => {
        if (times > 10) return null;
        return Math.min(times * 200, 5000);
      },
      lazyConnect: true,
    });

    this.redis.on('error', (err) => {
      this.logger.error(`Redis error: ${err.message}`);
    });
  }

  async onModuleInit(): Promise<void> {
    try {
      await this.redis.connect();
      this.logger.log('EntitiesService Redis connection established');
    } catch (error) {
      this.logger.warn(`Redis connection deferred: ${error}`);
    }

    try {
      await this.kafkaClient.connect();
      this.logger.log('EntitiesService Kafka client connected');
    } catch (error) {
      this.logger.warn(`Kafka connection deferred: ${error}`);
    }

    // Warm Redis cache from PostgreSQL
    await this.warmCache();
  }

  async onModuleDestroy(): Promise<void> {
    await this.redis.quit();
    await this.kafkaClient.close();
  }

  /**
   * Warms the Redis geospatial cache by loading all non-deleted entities
   * from PostgreSQL. Runs at startup so seeded data is immediately queryable.
   */
  private async warmCache(): Promise<void> {
    try {
      const rows = await this.dataSource.query(`
        SELECT
          id,
          entity_type AS "entityType",
          name,
          ST_Y(position::geometry) AS latitude,
          ST_X(position::geometry) AS longitude,
          heading,
          speed_knots AS speed,
          classification,
          source,
          affiliations AS affiliation,
          metadata,
          created_at AS "createdAt",
          updated_at AS "updatedAt"
        FROM sentinel.entities
        WHERE position IS NOT NULL
      `);

      if (rows.length === 0) {
        this.logger.log('Cache warm: no entities with positions found in DB');
        return;
      }

      const pipeline = this.redis.pipeline();

      for (const row of rows) {
        const entity: EntityRecord = {
          id: row.id,
          entityType: row.entityType,
          name: row.name,
          latitude: parseFloat(row.latitude),
          longitude: parseFloat(row.longitude),
          heading: row.heading != null ? parseFloat(row.heading) : undefined,
          speed: row.speed != null ? parseFloat(row.speed) : undefined,
          classification: row.classification,
          source: row.source,
          affiliation: Array.isArray(row.affiliation) ? row.affiliation.join(',') : row.affiliation,
          metadata: row.metadata ?? {},
          createdAt: row.createdAt instanceof Date ? row.createdAt.toISOString() : String(row.createdAt),
          updatedAt: row.updatedAt instanceof Date ? row.updatedAt.toISOString() : String(row.updatedAt),
        };

        // GEOADD for spatial queries
        pipeline.geoadd(ENTITY_GEO_KEY, entity.longitude, entity.latitude, entity.id);

        // Cache full entity JSON
        const cacheKey = `${ENTITY_CACHE_PREFIX}:${entity.id}`;
        pipeline.set(cacheKey, JSON.stringify(entity), 'EX', CACHE_TTL);
      }

      await pipeline.exec();
      this.logger.log(`Cache warm: loaded ${rows.length} entities from PostgreSQL into Redis`);
    } catch (error) {
      this.logger.warn(`Cache warm failed (DB may not be ready): ${error}`);
    }
  }

  /**
   * Queries entities within a bounding box using Redis GEOSEARCH
   * for fast spatial lookups, with optional type/source/classification filters.
   * Falls back to PostgreSQL when Redis returns no results.
   */
  async queryEntities(
    query: QueryEntitiesDto,
  ): Promise<PaginatedEntitiesResult> {
    const {
      north,
      south,
      east,
      west,
      entityTypes,
      sources,
      classification,
      page = 1,
      pageSize = 50,
    } = query;

    try {
      // Use Redis GEOSEARCH for spatial query if bounding box is provided
      let entityIds: string[] = [];

      if (
        north != null &&
        south != null &&
        east != null &&
        west != null
      ) {
        // Calculate center and dimensions for GEOSEARCH
        const centerLat = (north + south) / 2;
        const centerLng = (east + west) / 2;

        // Approximate width/height in km using Haversine
        const latDiff = Math.abs(north - south);
        const lngDiff = Math.abs(east - west);
        const heightKm = latDiff * 111.32;
        const widthKm =
          lngDiff * 111.32 * Math.cos((centerLat * Math.PI) / 180);

        // GEOSEARCH with FROMLONLAT and BYBOX
        entityIds = (await this.redis.geosearch(
          ENTITY_GEO_KEY,
          'FROMLONLAT',
          centerLng.toString(),
          centerLat.toString(),
          'BYBOX',
          widthKm.toString(),
          heightKm.toString(),
          'km',
          'ASC',
          'COUNT',
          '10000',
        )) as string[];
      } else {
        // No bounding box - return all cached entity IDs (with limit)
        const members = await this.redis.zrange(ENTITY_GEO_KEY, 0, 9999);
        entityIds = members;
      }

      // Fetch full entity data from cache
      let entities = await this.getEntitiesFromCache(entityIds);

      // If Redis returned nothing, fall back to PostgreSQL
      if (entities.length === 0) {
        entities = await this.queryEntitiesFromDb(query);
      }

      // Apply in-memory filters for type, source, classification
      if (entityTypes && entityTypes.length > 0) {
        const typeSet = new Set(
          entityTypes.map((t) => t.toUpperCase()),
        );
        entities = entities.filter((e) =>
          typeSet.has(e.entityType.toUpperCase()),
        );
      }

      if (sources && sources.length > 0) {
        const sourceSet = new Set(
          sources.map((s) => s.toUpperCase()),
        );
        entities = entities.filter((e) =>
          sourceSet.has(e.source.toUpperCase()),
        );
      }

      if (classification) {
        // Filter to entities at or below the requested classification
        const classHierarchy: Record<string, number> = {
          UNCLASSIFIED: 0,
          CONFIDENTIAL: 1,
          SECRET: 2,
          TOP_SECRET: 3,
        };
        const maxLevel = classHierarchy[classification.toUpperCase()] ?? 0;
        entities = entities.filter((e) => {
          const entityLevel =
            classHierarchy[e.classification.toUpperCase()] ?? 0;
          return entityLevel <= maxLevel;
        });
      }

      // Paginate
      const total = entities.length;
      const totalPages = Math.ceil(total / pageSize);
      const startIndex = (page - 1) * pageSize;
      const paginatedData = entities.slice(startIndex, startIndex + pageSize);

      return {
        data: paginatedData,
        pagination: {
          page,
          pageSize,
          total,
          totalPages,
        },
      };
    } catch (error) {
      this.logger.error(`Failed to query entities: ${error}`);
      // Return empty result on error rather than crashing
      return {
        data: [],
        pagination: { page: 1, pageSize, total: 0, totalPages: 0 },
      };
    }
  }

  /**
   * Falls back to a direct PostgreSQL query when Redis cache is empty.
   */
  private async queryEntitiesFromDb(
    query: QueryEntitiesDto,
  ): Promise<EntityRecord[]> {
    try {
      const { north, south, east, west } = query;

      let sql = `
        SELECT
          id,
          entity_type AS "entityType",
          name,
          ST_Y(position::geometry) AS latitude,
          ST_X(position::geometry) AS longitude,
          heading,
          speed_knots AS speed,
          classification,
          source,
          affiliations AS affiliation,
          metadata,
          created_at AS "createdAt",
          updated_at AS "updatedAt"
        FROM sentinel.entities
        WHERE position IS NOT NULL
      `;

      const params: (string | number)[] = [];

      if (north != null && south != null && east != null && west != null) {
        params.push(west, south, east, north);
        sql += ` AND ST_Within(position, ST_MakeEnvelope($1, $2, $3, $4, 4326))`;
      }

      sql += ' ORDER BY updated_at DESC LIMIT 500';

      const rows = await this.dataSource.query(sql, params);

      this.logger.log(`DB fallback: returned ${rows.length} entities from PostgreSQL`);

      // Re-warm cache with these results
      if (rows.length > 0) {
        const pipeline = this.redis.pipeline();
        for (const row of rows) {
          pipeline.geoadd(ENTITY_GEO_KEY, parseFloat(row.longitude), parseFloat(row.latitude), row.id);
          pipeline.set(
            `${ENTITY_CACHE_PREFIX}:${row.id}`,
            JSON.stringify(this.rowToEntity(row)),
            'EX',
            CACHE_TTL,
          );
        }
        pipeline.exec().catch((err) =>
          this.logger.warn(`Failed to re-warm cache from DB fallback: ${err}`),
        );
      }

      return rows.map((row: Record<string, unknown>) => this.rowToEntity(row));
    } catch (error) {
      this.logger.error(`DB fallback query failed: ${error}`);
      return [];
    }
  }

  /**
   * Converts a raw DB row to an EntityRecord.
   */
  private rowToEntity(row: Record<string, unknown>): EntityRecord {
    return {
      id: String(row['id']),
      entityType: String(row['entityType']),
      name: String(row['name']),
      latitude: parseFloat(String(row['latitude'])),
      longitude: parseFloat(String(row['longitude'])),
      heading: row['heading'] != null ? parseFloat(String(row['heading'])) : undefined,
      speed: row['speed'] != null ? parseFloat(String(row['speed'])) : undefined,
      classification: String(row['classification']),
      source: String(row['source']),
      affiliation: Array.isArray(row['affiliation']) ? (row['affiliation'] as string[]).join(',') : row['affiliation'] as string | undefined,
      metadata: (row['metadata'] as Record<string, unknown>) ?? {},
      createdAt: row['createdAt'] instanceof Date ? (row['createdAt'] as Date).toISOString() : String(row['createdAt']),
      updatedAt: row['updatedAt'] instanceof Date ? (row['updatedAt'] as Date).toISOString() : String(row['updatedAt']),
    };
  }

  /**
   * Retrieves a single entity by ID.
   */
  async getEntityById(id: string): Promise<Record<string, unknown>> {
    // Check cache first
    const cached = await this.getEntityFromCache(id);
    if (cached) {
      return cached as unknown as Record<string, unknown>;
    }

    // Fall back to DB
    try {
      const rows = await this.dataSource.query(
        `SELECT
          id,
          entity_type AS "entityType",
          name,
          ST_Y(position::geometry) AS latitude,
          ST_X(position::geometry) AS longitude,
          heading,
          speed_knots AS speed,
          classification,
          source,
          affiliations AS affiliation,
          metadata,
          created_at AS "createdAt",
          updated_at AS "updatedAt"
        FROM sentinel.entities
        WHERE id = $1`,
        [id],
      );

      if (rows.length > 0) {
        const entity = this.rowToEntity(rows[0]);
        // Cache for next time
        await this.cacheEntityPosition(entity);
        return entity as unknown as Record<string, unknown>;
      }
    } catch (error) {
      this.logger.warn(`DB lookup for entity ${id} failed: ${error}`);
    }

    // Emit a request to entity-service via Kafka and await response
    // For now, throw not found if not in cache or DB
    this.logger.debug(`Entity ${id} not in cache or DB, requesting from entity-service`);

    this.kafkaClient.emit('commands.entity.get', {
      key: id,
      value: { entityId: id },
    });

    throw new NotFoundException(`Entity ${id} not found`);
  }

  /**
   * Creates a new entity. Publishes a creation command to Kafka and
   * caches the initial position in Redis.
   */
  async createEntity(
    dto: CreateEntityDto,
  ): Promise<Record<string, unknown>> {
    const entityId = this.generateUUID();
    const now = new Date().toISOString();

    const entity: EntityRecord = {
      id: entityId,
      entityType: dto.entityType,
      name: dto.name,
      latitude: dto.latitude,
      longitude: dto.longitude,
      altitude: dto.altitude,
      heading: dto.heading,
      speed: dto.speed,
      classification: dto.classification ?? 'UNCLASSIFIED',
      source: dto.source,
      affiliation: dto.affiliation,
      metadata: dto.metadata,
      createdAt: now,
      updatedAt: now,
    };

    // Cache position in Redis geospatial index
    await this.cacheEntityPosition(entity);

    // Publish creation event to Kafka
    this.kafkaClient.emit('events.entity.created', {
      key: entityId,
      value: {
        entity_id: entityId,
        entity_type: entity.entityType,
        name: entity.name,
        latitude: entity.latitude,
        longitude: entity.longitude,
        altitude_meters: entity.altitude,
        classification: entity.classification,
        source: entity.source,
        affiliation: entity.affiliation,
        metadata: entity.metadata,
        timestamp: now,
      },
    });

    this.logger.log(
      `Entity created: ${entityId} (${entity.entityType}: ${entity.name})`,
    );

    return entity as unknown as Record<string, unknown>;
  }

  /**
   * Updates an existing entity. Publishes an update event to Kafka
   * and refreshes the Redis cache.
   */
  async updateEntity(
    id: string,
    dto: UpdateEntityDto,
  ): Promise<Record<string, unknown>> {
    const existing = await this.getEntityFromCache(id);
    if (!existing) {
      throw new NotFoundException(`Entity ${id} not found`);
    }

    const now = new Date().toISOString();
    const updated: EntityRecord = {
      ...existing,
      ...this.stripUndefined(dto as unknown as Record<string, unknown>),
      id, // Ensure ID is not overwritten
      updatedAt: now,
    };

    // Update Redis cache
    await this.cacheEntityPosition(updated);

    // Publish update event
    this.kafkaClient.emit('events.entity.updated', {
      key: id,
      value: {
        entity_id: id,
        entity_type: updated.entityType,
        name: updated.name,
        latitude: updated.latitude,
        longitude: updated.longitude,
        altitude_meters: updated.altitude,
        heading: updated.heading,
        speed_knots: updated.speed,
        classification: updated.classification,
        source: updated.source,
        timestamp: now,
      },
    });

    // If position changed, also emit a position event for real-time tracking
    if (dto.latitude != null || dto.longitude != null) {
      this.kafkaClient.emit('events.entity.position', {
        key: id,
        value: {
          entity_id: id,
          entity_type: updated.entityType,
          latitude: updated.latitude,
          longitude: updated.longitude,
          altitude_meters: updated.altitude,
          heading: updated.heading,
          speed_knots: updated.speed,
          classification: updated.classification,
          source: updated.source,
          timestamp: now,
        },
      });
    }

    this.logger.log(`Entity updated: ${id}`);

    return updated as unknown as Record<string, unknown>;
  }

  /**
   * Soft-deletes an entity. Removes from Redis cache and publishes
   * a deletion event to Kafka.
   */
  async deleteEntity(id: string): Promise<void> {
    // Remove from geo index
    await this.redis.zrem(ENTITY_GEO_KEY, id);

    // Remove cached data
    await this.redis.del(`${ENTITY_CACHE_PREFIX}:${id}`);

    // Publish deletion event
    this.kafkaClient.emit('events.entity.deleted', {
      key: id,
      value: {
        entity_id: id,
        timestamp: new Date().toISOString(),
      },
    });

    this.logger.log(`Entity deleted: ${id}`);
  }

  /**
   * Caches an entity's position in the Redis geospatial index
   * and stores the full entity data as a JSON hash.
   */
  private async cacheEntityPosition(entity: EntityRecord): Promise<void> {
    try {
      // GEOADD for spatial queries
      await this.redis.geoadd(
        ENTITY_GEO_KEY,
        entity.longitude,
        entity.latitude,
        entity.id,
      );

      // Store full entity data
      const cacheKey = `${ENTITY_CACHE_PREFIX}:${entity.id}`;
      await this.redis.set(
        cacheKey,
        JSON.stringify(entity),
        'EX',
        CACHE_TTL,
      );
    } catch (error) {
      this.logger.error(
        `Failed to cache entity ${entity.id}: ${error}`,
      );
    }
  }

  /**
   * Retrieves a single entity from the Redis cache.
   */
  private async getEntityFromCache(
    id: string,
  ): Promise<EntityRecord | null> {
    try {
      const cacheKey = `${ENTITY_CACHE_PREFIX}:${id}`;
      const data = await this.redis.get(cacheKey);
      if (!data) return null;
      return JSON.parse(data) as EntityRecord;
    } catch (error) {
      this.logger.error(
        `Failed to get entity ${id} from cache: ${error}`,
      );
      return null;
    }
  }

  /**
   * Batch-retrieves entities from cache by their IDs.
   */
  private async getEntitiesFromCache(
    ids: string[],
  ): Promise<EntityRecord[]> {
    if (ids.length === 0) return [];

    try {
      const pipeline = this.redis.pipeline();
      for (const id of ids) {
        pipeline.get(`${ENTITY_CACHE_PREFIX}:${id}`);
      }

      const results = await pipeline.exec();
      if (!results) return [];

      const entities: EntityRecord[] = [];
      for (const [err, result] of results) {
        if (!err && result && typeof result === 'string') {
          try {
            entities.push(JSON.parse(result) as EntityRecord);
          } catch {
            // Skip malformed entries
          }
        }
      }

      return entities;
    } catch (error) {
      this.logger.error(`Failed to batch-get entities from cache: ${error}`);
      return [];
    }
  }

  /**
   * Generates a v4-style UUID.
   */
  private generateUUID(): string {
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(
      /[xy]/g,
      (c) => {
        const r = (Math.random() * 16) | 0;
        const v = c === 'x' ? r : (r & 0x3) | 0x8;
        return v.toString(16);
      },
    );
  }

  /**
   * Strips undefined values from an object to avoid overwriting
   * existing fields with undefined during partial updates.
   */
  private stripUndefined(
    obj: Record<string, unknown>,
  ): Record<string, unknown> {
    const result: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(obj)) {
      if (value !== undefined) {
        result[key] = value;
      }
    }
    return result;
  }
}
