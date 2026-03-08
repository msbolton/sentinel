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
      await this.dataSource.query(
        `CREATE INDEX IF NOT EXISTS idx_entities_source_entity_id ON sentinel.entities ((metadata->>'sourceEntityId'))`,
      );
      this.logger.log('Ensured idx_entities_source_entity_id index exists');
    } catch (error) {
      this.logger.warn(`Failed to create sourceEntityId index: ${error}`);
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
          this.entityService.emitPositionEvent(u);
          await this.entityService.updateRedisGeo(u.id, u.lng, u.lat);
        }
      }

      // 5. Create new entities (individually — less frequent, needs full entity creation logic)
      for (const msg of newEntities) {
        try {
          const entityType = mapEntityType(msg.entity_type);
          const entitySource = inferEntitySource(msg.entity_id, msg.source);
          const hasPosition = msg.latitude !== 0 || msg.longitude !== 0;

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
            metadata: { sourceEntityId: msg.entity_id },
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
