import { Controller, Logger, OnModuleInit } from '@nestjs/common';
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
  latitude: number;
  longitude: number;
  altitude: number;
  heading: number;
  speed_knots: number;
  course: number;
  timestamp: string;
}

@Controller()
export class IngestConsumer implements OnModuleInit {
  private readonly logger = new Logger(IngestConsumer.name);

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
  }

  @EventPattern('ingest.raw')
  async handleIngestMessage(@Payload() message: IngestMessage): Promise<void> {
    try {
      const entityType = mapEntityType(message.entity_type);
      const entitySource = inferEntitySource(message.entity_id);
      const hasPosition = message.latitude !== 0 || message.longitude !== 0;

      const existing = await this.findBySourceEntityId(message.entity_id);

      if (existing) {
        // Update position if we have valid coordinates.
        if (hasPosition) {
          await this.entityService.updatePosition(existing.id, {
            lat: message.latitude,
            lng: message.longitude,
            heading: message.heading || undefined,
            speedKnots: message.speed_knots || undefined,
            course: message.course || undefined,
          });
        }

        // Update name if it changed and is non-empty.
        if (message.name && message.name !== existing.name) {
          await this.entityService.update(existing.id, { name: message.name });
        }

        return;
      }

      // Create new entity.
      await this.entityService.create({
        entityType,
        name: message.name || message.entity_id,
        source: entitySource,
        classification: Classification.UNCLASSIFIED,
        position: hasPosition
          ? { lat: message.latitude, lng: message.longitude }
          : undefined,
        heading: message.heading || undefined,
        speedKnots: message.speed_knots || undefined,
        course: message.course || undefined,
        metadata: { sourceEntityId: message.entity_id },
      });
    } catch (error) {
      this.logger.error(
        `Failed to process ingest message for ${message.entity_id}: ${error}`,
      );
    }
  }

  private async findBySourceEntityId(
    sourceEntityId: string,
  ): Promise<{ id: string; name: string } | null> {
    const result = await this.entityRepository
      .createQueryBuilder('e')
      .select(['e.id', 'e.name'])
      .where('e.deleted = :deleted', { deleted: false })
      .andWhere("e.metadata->>'sourceEntityId' = :sourceEntityId", {
        sourceEntityId,
      })
      .getOne();

    return result ? { id: result.id, name: result.name } : null;
  }
}

function mapEntityType(raw: string): EntityType {
  const mapping: Record<string, EntityType> = {
    aircraft: EntityType.AIRCRAFT,
    vessel: EntityType.VESSEL,
    vehicle: EntityType.VEHICLE,
    person: EntityType.PERSON,
    satellite: EntityType.SATELLITE,
    sensor: EntityType.EQUIPMENT,
    platform: EntityType.EQUIPMENT,
  };
  return mapping[raw?.toLowerCase()] ?? EntityType.UNKNOWN;
}

function inferEntitySource(entityId: string): EntitySource {
  if (entityId.startsWith('ICAO-')) return EntitySource.ADS_B;
  if (entityId.startsWith('MMSI-')) return EntitySource.AIS;
  if (entityId.startsWith('JTN-')) return EntitySource.LINK16;
  if (entityId.startsWith('SAT-')) return EntitySource.CELESTRAK;
  return EntitySource.GPS;
}
