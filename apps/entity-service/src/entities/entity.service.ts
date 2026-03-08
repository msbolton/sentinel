import {
  Injectable,
  Logger,
  NotFoundException,
  Inject,
  OnModuleInit,
} from '@nestjs/common';
import { ClientKafka } from '@nestjs/microservices';
import { ConfigService } from '@nestjs/config';
import { EntityRecord } from './entity.entity';
import { EntityRepository, EntityWithDistance, EntityCountByType } from './entity.repository';
import { CreateEntityDto } from './dto/create-entity.dto';
import { UpdateEntityDto } from './dto/update-entity.dto';
import { UpdatePositionDto } from './dto/update-position.dto';
import { QueryEntitiesDto } from './dto/query-entities.dto';
import { CLASSIFICATION_ORDER } from './enums';
import Redis from 'ioredis';

/** Kafka topic constants */
const TOPIC_ENTITY_CREATED = 'events.entity.created';
const TOPIC_ENTITY_UPDATED = 'events.entity.updated';
const TOPIC_ENTITY_POSITION = 'events.entity.position';
const TOPIC_ENTITY_DELETED = 'events.entity.deleted';

/** Redis key for the geospatial index */
const REDIS_GEO_KEY = 'sentinel:entities:geo';

@Injectable()
export class EntityService implements OnModuleInit {
  private readonly logger = new Logger(EntityService.name);
  private redis: Redis | null = null;

  constructor(
    private readonly entityRepository: EntityRepository,
    @Inject('KAFKA_CLIENT') private readonly kafkaClient: ClientKafka,
    private readonly configService: ConfigService,
  ) {}

  async onModuleInit(): Promise<void> {
    // Initialize Redis connection for geospatial caching
    const redisHost = this.configService.get<string>('REDIS_HOST', 'localhost');
    const redisPort = this.configService.get<number>('REDIS_PORT', 6379);
    const redisPassword = this.configService.get<string>('REDIS_PASSWORD', '');

    try {
      this.redis = new Redis({
        host: redisHost,
        port: redisPort,
        password: redisPassword || undefined,
        lazyConnect: true,
        retryStrategy: (times: number) => Math.min(times * 100, 3000),
      });
      await this.redis.connect();
      this.logger.log(`Redis connected at ${redisHost}:${redisPort}`);
    } catch (error) {
      this.logger.warn(
        `Redis connection failed (${redisHost}:${redisPort}). Geospatial cache disabled. Error: ${error}`,
      );
      this.redis = null;
    }
  }

  // ─── CREATE ───────────────────────────────────────────────────────────

  async create(dto: CreateEntityDto): Promise<EntityRecord> {
    const entity = this.entityRepository.create({
      entityType: dto.entityType,
      name: dto.name,
      description: dto.description,
      source: dto.source,
      classification: dto.classification,
      feedId: dto.feedId ?? null,
      heading: dto.heading ?? null,
      speedKnots: dto.speedKnots ?? null,
      course: dto.course ?? null,
      altitude: dto.altitude ?? null,
      milStd2525dSymbol: dto.milStd2525dSymbol ?? null,
      metadata: dto.metadata ?? {},
      affiliations: dto.affiliations ?? [],
      lastSeenAt: dto.position ? new Date() : null,
    });

    // Set position as GeoJSON if provided
    if (dto.position) {
      entity.position = {
        type: 'Point',
        coordinates: [dto.position.lng, dto.position.lat],
      };
    }

    const saved = await this.entityRepository.save(entity);

    // Publish to Kafka in api-gateway compatible format
    if (dto.position) {
      this.emitKafka(TOPIC_ENTITY_CREATED, saved.id, {
        entity_id: saved.id,
        entity_type: saved.entityType,
        latitude: dto.position.lat,
        longitude: dto.position.lng,
        altitude_meters: dto.altitude ?? dto.position.altitude ?? undefined,
        heading: saved.heading,
        speed_knots: saved.speedKnots,
        classification: saved.classification,
        source: saved.source,
        timestamp: saved.lastSeenAt?.toISOString() ?? new Date().toISOString(),
        metadata: saved.metadata,
      });
    }

    // Cache in Redis geospatial index
    if (dto.position) {
      await this.redisGeoAdd(saved.id, dto.position.lng, dto.position.lat);
    }

    this.logger.log(`Entity created: ${saved.id} (${saved.entityType}/${saved.name})`);
    return saved;
  }

  // ─── UPDATE ───────────────────────────────────────────────────────────

  async update(id: string, dto: UpdateEntityDto): Promise<EntityRecord> {
    const existing = await this.findByIdOrThrow(id);

    // Build partial update
    const updates: Partial<EntityRecord> = {};

    if (dto.entityType !== undefined) updates.entityType = dto.entityType;
    if (dto.name !== undefined) updates.name = dto.name;
    if (dto.description !== undefined) updates.description = dto.description;
    if (dto.source !== undefined) updates.source = dto.source;
    if (dto.classification !== undefined) updates.classification = dto.classification;
    if (dto.heading !== undefined) updates.heading = dto.heading;
    if (dto.speedKnots !== undefined) updates.speedKnots = dto.speedKnots;
    if (dto.course !== undefined) updates.course = dto.course;
    if (dto.altitude !== undefined) updates.altitude = dto.altitude;
    if (dto.milStd2525dSymbol !== undefined) updates.milStd2525dSymbol = dto.milStd2525dSymbol;
    if (dto.metadata !== undefined) updates.metadata = dto.metadata;
    if (dto.affiliations !== undefined) updates.affiliations = dto.affiliations;

    if (dto.position) {
      updates.position = {
        type: 'Point',
        coordinates: [dto.position.lng, dto.position.lat],
      };
      updates.lastSeenAt = new Date();
    }

    Object.assign(existing, updates);
    const saved = await this.entityRepository.save(existing);

    // Publish to Kafka in api-gateway compatible format (only if position is available)
    const coords = saved.position as any;
    if (coords?.coordinates && Array.isArray(coords.coordinates)) {
      this.emitKafka(TOPIC_ENTITY_UPDATED, saved.id, {
        entity_id: saved.id,
        entity_type: saved.entityType,
        latitude: coords.coordinates[1],
        longitude: coords.coordinates[0],
        altitude_meters: saved.altitude ?? undefined,
        heading: saved.heading,
        speed_knots: saved.speedKnots,
        classification: saved.classification,
        source: saved.source,
        timestamp: saved.lastSeenAt?.toISOString() ?? new Date().toISOString(),
        metadata: saved.metadata,
      });
    }

    // Update Redis geospatial index if position changed
    if (dto.position) {
      await this.redisGeoAdd(saved.id, dto.position.lng, dto.position.lat);
    }

    this.logger.log(`Entity updated: ${saved.id}`);
    return saved;
  }

  // ─── READ ─────────────────────────────────────────────────────────────

  async findById(id: string): Promise<EntityRecord> {
    return this.findByIdOrThrow(id);
  }

  async findWithinBoundingBox(
    query: QueryEntitiesDto,
  ): Promise<{ data: EntityRecord[]; total: number; page: number; pageSize: number }> {
    const page = query.page ?? 1;
    const pageSize = query.pageSize ?? 100;
    const offset = (page - 1) * pageSize;

    // If bounding box params are not provided, fall back to a non-spatial query
    if (
      query.north === undefined ||
      query.south === undefined ||
      query.east === undefined ||
      query.west === undefined
    ) {
      return this.findFiltered(query, pageSize, offset, page);
    }

    const [data, total] = await this.entityRepository.findWithinBoundingBox({
      north: query.north,
      south: query.south,
      east: query.east,
      west: query.west,
      entityTypes: query.types,
      sources: query.sources,
      maxClassification: query.classification,
      limit: pageSize,
      offset,
    });

    return { data, total, page, pageSize };
  }

  async findNearby(
    lat: number,
    lng: number,
    radiusMeters: number,
  ): Promise<EntityWithDistance[]> {
    return this.entityRepository.findNearby({ lat, lng, radiusMeters });
  }

  // ─── POSITION UPDATE ─────────────────────────────────────────────────

  async updatePosition(id: string, dto: UpdatePositionDto): Promise<EntityRecord> {
    const updated = await this.entityRepository.updatePosition(
      id,
      dto.lat,
      dto.lng,
      dto.heading,
      dto.speedKnots,
      dto.course,
      dto.altitude,
    );

    if (!updated) {
      throw new NotFoundException(`Entity ${id} not found after position update`);
    }

    // Publish position event to Kafka in api-gateway compatible format
    this.emitKafka(TOPIC_ENTITY_POSITION, updated.id, {
      entity_id: updated.id,
      entity_type: updated.entityType,
      latitude: dto.lat,
      longitude: dto.lng,
      altitude_meters: dto.altitude ?? updated.altitude ?? undefined,
      heading: dto.heading,
      speed_knots: dto.speedKnots,
      classification: updated.classification,
      source: updated.source,
      timestamp: updated.lastSeenAt?.toISOString() ?? new Date().toISOString(),
      metadata: updated.metadata,
    });

    // Update Redis geospatial index
    await this.redisGeoAdd(id, dto.lng, dto.lat);

    this.logger.debug(`Position updated for entity ${id}: [${dto.lat}, ${dto.lng}]`);
    return updated;
  }

  // ─── DELETE ───────────────────────────────────────────────────────────

  async delete(id: string): Promise<void> {
    const exists = await this.entityRepository.findActiveById(id);
    if (!exists) {
      throw new NotFoundException(`Entity with ID "${id}" not found`);
    }

    const deleted = await this.entityRepository.softDeleteEntity(id);
    if (!deleted) {
      throw new NotFoundException(`Entity with ID "${id}" could not be deleted`);
    }

    // Publish to Kafka in api-gateway compatible format
    this.emitKafka(TOPIC_ENTITY_DELETED, id, {
      entity_id: id,
      timestamp: new Date().toISOString()
    });

    // Remove from Redis geospatial index
    await this.redisGeoRemove(id);

    this.logger.log(`Entity soft-deleted: ${id}`);
  }

  // ─── STATS ────────────────────────────────────────────────────────────

  async getEntityCount(): Promise<EntityCountByType[]> {
    return this.entityRepository.getEntityCounts();
  }

  // ─── Batch helpers (used by IngestConsumer) ──────────────────────────

  emitPositionEvent(update: {
    id: string;
    lat: number;
    lng: number;
    heading: number | null;
    speedKnots: number | null;
    course: number | null;
    altitude: number | null;
    entityType?: string;
    classification?: string;
    source?: string;
    metadata?: Record<string, unknown>;
    trackEnvironment?: string;
    platformData?: Record<string, unknown> | null;
    circularError?: number | null;
  }): void {
    this.emitKafka(TOPIC_ENTITY_POSITION, update.id, {
      entity_id: update.id,
      entity_type: update.entityType,
      latitude: update.lat,
      longitude: update.lng,
      altitude_meters: update.altitude ?? undefined,
      heading: update.heading,
      speed_knots: update.speedKnots,
      classification: update.classification,
      source: update.source,
      timestamp: new Date().toISOString(),
      metadata: update.metadata,
      track_environment: update.trackEnvironment,
      platform_data: update.platformData,
      circular_error: update.circularError,
    });
  }

  async updateRedisGeo(entityId: string, lng: number, lat: number): Promise<void> {
    await this.redisGeoAdd(entityId, lng, lat);
  }

  // ─── Private helpers ─────────────────────────────────────────────────

  private async findByIdOrThrow(id: string): Promise<EntityRecord> {
    const entity = await this.entityRepository.findActiveById(id);
    if (!entity) {
      throw new NotFoundException(`Entity with ID "${id}" not found`);
    }
    return entity;
  }

  /**
   * Non-spatial filtered query when bounding box params are absent.
   */
  private async findFiltered(
    query: QueryEntitiesDto,
    limit: number,
    offset: number,
    page: number,
  ): Promise<{ data: EntityRecord[]; total: number; page: number; pageSize: number }> {
    const qb = this.entityRepository.createQueryBuilder('e')
      .where('e.deleted = :deleted', { deleted: false });

    if (query.types && query.types.length > 0) {
      qb.andWhere('e.entityType IN (:...types)', { types: query.types });
    }

    if (query.sources && query.sources.length > 0) {
      qb.andWhere('e.source IN (:...sources)', { sources: query.sources });
    }

    if (query.classification) {
      const allowedLevels = CLASSIFICATION_ORDER.slice(
        0,
        CLASSIFICATION_ORDER.indexOf(query.classification) + 1,
      );
      qb.andWhere('e.classification IN (:...allowedLevels)', { allowedLevels });
    }

    qb.orderBy('e.lastSeenAt', 'DESC', 'NULLS LAST')
      .skip(offset)
      .take(limit);

    const [data, total] = await qb.getManyAndCount();
    return { data, total, page, pageSize: limit };
  }

  /**
   * Emit a Kafka event (fire-and-forget with error logging).
   */
  private emitKafka(topic: string, key: string, payload: unknown): void {
    try {
      this.kafkaClient.emit(topic, {
        key,
        value: JSON.stringify(payload),
        headers: {
          'sentinel-service': 'entity-service',
          'sentinel-timestamp': new Date().toISOString(),
        },
      });
    } catch (error) {
      this.logger.error(`Failed to emit Kafka event to ${topic}: ${error}`);
    }
  }

  /**
   * Add/update an entity in the Redis geospatial index (GEOADD).
   */
  private async redisGeoAdd(entityId: string, lng: number, lat: number): Promise<void> {
    if (!this.redis) return;
    try {
      await this.redis.geoadd(REDIS_GEO_KEY, lng, lat, entityId);
    } catch (error) {
      this.logger.warn(`Redis GEOADD failed for entity ${entityId}: ${error}`);
    }
  }

  /**
   * Remove an entity from the Redis geospatial index.
   */
  private async redisGeoRemove(entityId: string): Promise<void> {
    if (!this.redis) return;
    try {
      await this.redis.zrem(REDIS_GEO_KEY, entityId);
    } catch (error) {
      this.logger.warn(`Redis ZREM failed for entity ${entityId}: ${error}`);
    }
  }
}
