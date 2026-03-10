import { Controller, Logger, OnModuleInit, OnModuleDestroy } from '@nestjs/common';
import { EventPattern, Payload } from '@nestjs/microservices';
import { DataSource } from 'typeorm';
import { EntityService } from './entity.service';
import { EntityRepository } from './entity.repository';
import { EntityType, EntitySource, Classification } from './enums';

interface IngestMessage {
  entity_id: string;
  entity_type: string;
  name: string;
  source: string;
  feed_id?: string;
  latitude: number;
  longitude: number;
  altitude: number;
  heading: number;
  speed_knots: number;
  course: number;
  timestamp: string;

  pitch?: number;
  roll?: number;
  track_environment?: string;
  affiliation?: string;
  operational_status?: string;
  country_of_origin?: string;

  // Velocity decomposition (m/s, North-East-Up)
  velocity_north?: number;
  velocity_east?: number;
  velocity_up?: number;

  // Acceleration (m/s²)
  accel_north?: number;
  accel_east?: number;
  accel_up?: number;

  // Measurement uncertainty
  circular_error?: number;

  // Covariance matrices (upper triangle arrays)
  pos_covariance?: number[];
  pos_vel_covariance?: number[];
  vel_covariance?: number[];

  // Physical dimensions (meters)
  dimension_length?: number;
  dimension_width?: number;

  // Protocol-specific typed data (only one populated per message)
  ais_data?: Record<string, unknown>;
  adsb_data?: Record<string, unknown>;
  tle_data?: Record<string, unknown>;
  link16_data?: Record<string, unknown>;
  cot_data?: Record<string, unknown>;
  uav_data?: Record<string, unknown>;
}

@Controller()
export class IngestConsumer implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(IngestConsumer.name);

  private messageBuffer: IngestMessage[] = [];
  private flushTimer: ReturnType<typeof setInterval> | null = null;
  private static readonly FLUSH_INTERVAL_MS = 100;
  private static readonly MAX_BATCH_SIZE = 200;

  constructor(
    private readonly entityService: EntityService,
    private readonly entityRepository: EntityRepository,
    private readonly dataSource: DataSource,
  ) {}

  async onModuleInit(): Promise<void> {
    try {
      // Legacy JSONB index (kept for backwards compat during transition)
      await this.dataSource.query(
        `CREATE INDEX IF NOT EXISTS idx_entities_source_entity_id ON sentinel.entities ((metadata->>'sourceEntityId'))`,
      );
      // GIN index on platformData for JSONB queries
      await this.dataSource.query(
        `CREATE INDEX IF NOT EXISTS idx_entities_platform_data ON sentinel.entities USING GIN ("platformData")`,
      );

      // Backfill sourceEntityId column from metadata JSONB
      const backfilled = await this.dataSource.query(
        `UPDATE sentinel.entities SET "sourceEntityId" = metadata->>'sourceEntityId'
         WHERE metadata->>'sourceEntityId' IS NOT NULL AND "sourceEntityId" IS NULL`,
      );
      if (backfilled?.[1] > 0) {
        this.logger.log(`Backfilled sourceEntityId for ${backfilled[1]} entities`);
      }

      // Backfill trackEnvironment from entityType
      await this.dataSource.query(
        `UPDATE sentinel.entities SET "trackEnvironment" = CASE "entityType"
           WHEN 'AIRCRAFT' THEN 'AIR' WHEN 'DRONE' THEN 'AIR'
           WHEN 'VESSEL' THEN 'SEA_SURFACE'
           WHEN 'SATELLITE' THEN 'SPACE'
           WHEN 'VEHICLE' THEN 'GROUND' WHEN 'PERSON' THEN 'GROUND'
           ELSE 'UNKNOWN' END
         WHERE "trackEnvironment" = 'UNKNOWN' AND "entityType" != 'UNKNOWN'`,
      );

      this.logger.log('Ensured entity enrichment indexes and backfills complete');
    } catch (error) {
      this.logger.warn(`Failed during onModuleInit migrations: ${error}`);
    }

    this.flushTimer = setInterval(
      () => this.flushBuffer(),
      IngestConsumer.FLUSH_INTERVAL_MS,
    );
  }

  async onModuleDestroy(): Promise<void> {
    if (this.flushTimer) clearInterval(this.flushTimer);
    await this.flushBuffer();
  }

  @EventPattern('ingest.raw')
  async handleIngestMessage(@Payload() message: IngestMessage): Promise<void> {
    this.messageBuffer.push(message);
    if (this.messageBuffer.length >= IngestConsumer.MAX_BATCH_SIZE) {
      await this.flushBuffer();
    }
  }

  async flushBuffer(): Promise<void> {
    if (this.messageBuffer.length === 0) return;

    const batch = this.messageBuffer;
    this.messageBuffer = [];

    try {
      // 1. Collect all source entity IDs
      const sourceIds = batch.map((m) => m.entity_id);

      // 2. Single bulk lookup
      const existingMap =
        await this.entityRepository.findBySourceEntityIds(sourceIds);

      // 3. Partition into new vs existing
      const newEntities: IngestMessage[] = [];
      const positionUpdates: Array<{
        id: string;
        lat: number;
        lng: number;
        heading: number | null;
        speedKnots: number | null;
        course: number | null;
        altitude: number | null;
        entityType: string;
        classification: string;
        source: string;
        metadata: Record<string, unknown>;
        platformData?: Record<string, unknown> | null;
        kinematics?: Record<string, unknown> | null;
        trackEnvironment?: string | null;
        circularError?: number | null;
      }> = [];
      const nameUpdates: Array<{ id: string; name: string }> = [];

      for (const msg of batch) {
        const existing = existingMap.get(msg.entity_id);
        const hasPosition = msg.latitude !== 0 || msg.longitude !== 0;

        if (existing) {
          if (hasPosition) {
            positionUpdates.push({
              id: existing.id,
              lat: msg.latitude,
              lng: msg.longitude,
              heading: msg.heading || null,
              speedKnots: msg.speed_knots || null,
              course: msg.course || null,
              altitude: msg.altitude || null,
              entityType: existing.entityType,
              classification: existing.classification,
              source: existing.source,
              metadata: existing.metadata,
              platformData: buildPlatformData(msg),
              kinematics: buildKinematics(msg),
              trackEnvironment: msg.track_environment || null,
              circularError: msg.circular_error || null,
            });
          }
          if (msg.name && msg.name !== existing.name) {
            nameUpdates.push({ id: existing.id, name: msg.name });
          }
        } else {
          newEntities.push(msg);
        }
      }

      // 4. Bulk position update (single SQL)
      if (positionUpdates.length > 0) {
        await this.entityRepository.bulkUpdatePositions(positionUpdates);
        for (const u of positionUpdates) {
          this.entityService.emitPositionEvent({
            ...u,
            trackEnvironment: u.trackEnvironment ?? undefined,
            platformData: u.platformData ?? undefined,
            circularError: u.circularError ?? undefined,
          });
          await this.entityService.updateRedisGeo(u.id, u.lng, u.lat);
        }
      }

      // 5. Create new entities (individually — less frequent, needs full entity creation logic)
      for (const msg of newEntities) {
        try {
          const entityType = mapEntityType(msg.entity_type);
          const entitySource = inferEntitySource(msg.entity_id, msg.source);
          const hasPosition = msg.latitude !== 0 || msg.longitude !== 0;

          const platformData = buildPlatformData(msg);
          const kinematics = buildKinematics(msg);

          await this.entityService.create({
            entityType,
            name: msg.name || msg.entity_id,
            source: entitySource,
            classification: Classification.UNCLASSIFIED,
            feedId: msg.feed_id || undefined,
            position: hasPosition
              ? {
                  lat: msg.latitude,
                  lng: msg.longitude,
                  altitude: msg.altitude || undefined,
                }
              : undefined,
            heading: msg.heading || undefined,
            speedKnots: msg.speed_knots || undefined,
            course: msg.course || undefined,
            altitude: msg.altitude || undefined,
            metadata: {
              sourceEntityId: msg.entity_id,
              ...(platformData ? { platformData } : {}),
              ...(kinematics ? { kinematics } : {}),
              ...(msg.track_environment ? { trackEnvironment: msg.track_environment } : {}),
              ...(msg.country_of_origin ? { countryOfOrigin: msg.country_of_origin } : {}),
              ...(msg.circular_error ? { circularError: msg.circular_error } : {}),
              ...(msg.dimension_length ? { dimensionLength: msg.dimension_length } : {}),
              ...(msg.dimension_width ? { dimensionWidth: msg.dimension_width } : {}),
            },
          });
        } catch (error) {
          this.logger.error(
            `Failed to create entity ${msg.entity_id}: ${error}`,
          );
        }
      }

      // 6. Name updates (individually — infrequent)
      for (const { id, name } of nameUpdates) {
        try {
          await this.entityService.update(id, { name });
        } catch (error) {
          this.logger.error(`Failed to update name for ${id}: ${error}`);
        }
      }

      this.logger.debug(
        `Batch processed: ${positionUpdates.length} positions, ${newEntities.length} new, ${nameUpdates.length} names`,
      );
    } catch (error) {
      this.logger.error(`Batch processing failed: ${error}`);
    }
  }
}

function mapEntityType(raw: string): EntityType {
  const mapping: Record<string, EntityType> = {
    aircraft: EntityType.AIRCRAFT,
    vessel: EntityType.VESSEL,
    vehicle: EntityType.VEHICLE,
    person: EntityType.PERSON,
    satellite: EntityType.SATELLITE,
    drone: EntityType.DRONE,
    sensor: EntityType.EQUIPMENT,
    platform: EntityType.EQUIPMENT,
  };
  return mapping[raw?.toLowerCase()] ?? EntityType.UNKNOWN;
}

/**
 * Convert snake_case keys to camelCase (Go JSON tags → TypeScript conventions).
 */
function snakeToCamel(obj: Record<string, unknown>): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(obj)) {
    const camelKey = key.replace(/_([a-z])/g, (_, c) => c.toUpperCase());
    result[camelKey] = value;
  }
  return result;
}

/**
 * Build the platformData discriminated union from protocol-specific fields.
 */
function buildPlatformData(msg: IngestMessage): Record<string, unknown> | null {
  if (msg.ais_data) return { ais: snakeToCamel(msg.ais_data) };
  if (msg.adsb_data) return { adsb: snakeToCamel(msg.adsb_data) };
  if (msg.tle_data) return { tle: snakeToCamel(msg.tle_data) };
  if (msg.link16_data) return { link16: snakeToCamel(msg.link16_data) };
  if (msg.cot_data) return { cot: snakeToCamel(msg.cot_data) };
  if (msg.uav_data) return { uav: snakeToCamel(msg.uav_data) };
  return null;
}

/**
 * Build kinematic state from velocity/acceleration/covariance fields.
 */
function buildKinematics(msg: IngestMessage): Record<string, unknown> | null {
  const hasVelocity = msg.velocity_north || msg.velocity_east || msg.velocity_up;
  const hasAccel = msg.accel_north || msg.accel_east || msg.accel_up;
  const hasCovariance = msg.pos_covariance || msg.vel_covariance || msg.pos_vel_covariance;

  if (!hasVelocity && !hasAccel && !hasCovariance) return null;

  const kin: Record<string, unknown> = {};

  if (hasVelocity) {
    kin.velocity = {
      north: msg.velocity_north ?? 0,
      east: msg.velocity_east ?? 0,
      up: msg.velocity_up ?? 0,
    };
  }

  if (hasAccel) {
    kin.acceleration = {
      north: msg.accel_north ?? 0,
      east: msg.accel_east ?? 0,
      up: msg.accel_up ?? 0,
    };
  }

  if (msg.pos_covariance && msg.pos_covariance.length === 6) {
    const c = msg.pos_covariance;
    kin.positionCovariance = { pnPn: c[0], pnPe: c[1], pnPu: c[2], pePe: c[3], pePu: c[4], puPu: c[5] };
  }

  if (msg.pos_vel_covariance && msg.pos_vel_covariance.length === 9) {
    const c = msg.pos_vel_covariance;
    kin.positionVelocityCovariance = {
      pnVn: c[0], pnVe: c[1], pnVu: c[2],
      peVn: c[3], peVe: c[4], peVu: c[5],
      puVn: c[6], puVe: c[7], puVu: c[8],
    };
  }

  if (msg.vel_covariance && msg.vel_covariance.length === 6) {
    const c = msg.vel_covariance;
    kin.velocityCovariance = { vnVn: c[0], vnVe: c[1], vnVu: c[2], veVe: c[3], veVu: c[4], vuVu: c[5] };
  }

  return kin;
}

function inferEntitySource(entityId: string, source?: string): EntitySource {
  // Prefer explicit source from ingest adapter when available.
  if (source) {
    const sourceMapping: Record<string, EntitySource> = {
      opensky: EntitySource.OPENSKY,
      adsblol: EntitySource.ADSB_LOL,
      celestrak: EntitySource.CELESTRAK,
      ais: EntitySource.AIS,
      adsb: EntitySource.ADS_B,
    };
    const mapped = sourceMapping[source.toLowerCase()];
    if (mapped) return mapped;
  }

  // Fall back to entity ID prefix inference.
  if (entityId.startsWith('ICAO-')) return EntitySource.ADS_B;
  if (entityId.startsWith('MMSI-')) return EntitySource.AIS;
  if (entityId.startsWith('JTN-')) return EntitySource.LINK16;
  if (entityId.startsWith('SAT-')) return EntitySource.CELESTRAK;
  return EntitySource.GPS;
}
