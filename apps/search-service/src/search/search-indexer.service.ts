import { Injectable, Logger } from '@nestjs/common';
import { MessagePattern, Payload } from '@nestjs/microservices';
import { OpenSearchService, EntityDocument } from './opensearch.service';

/**
 * Kafka consumer that automatically indexes entities into OpenSearch
 * when they are created or updated.
 */
@Injectable()
export class SearchIndexerService {
  private readonly logger = new Logger(SearchIndexerService.name);

  constructor(private readonly openSearchService: OpenSearchService) {}

  /**
   * Handle entity creation events from Kafka.
   */
  @MessagePattern('events.entity.created')
  async handleEntityCreated(
    @Payload() payload: EntityEventPayload,
  ): Promise<void> {
    this.logger.debug(`Indexing newly created entity: ${payload.id}`);

    try {
      const doc = this.mapPayloadToDocument(payload);
      await this.openSearchService.indexEntity(doc);
      this.logger.log(`Indexed new entity ${payload.id}: ${payload.name}`);
    } catch (error) {
      this.logger.error(
        `Failed to index created entity ${payload.id}`,
        error instanceof Error ? error.stack : String(error),
      );
    }
  }

  /**
   * Handle entity update events from Kafka.
   */
  @MessagePattern('events.entity.updated')
  async handleEntityUpdated(
    @Payload() payload: EntityEventPayload,
  ): Promise<void> {
    this.logger.debug(`Re-indexing updated entity: ${payload.id}`);

    try {
      const doc = this.mapPayloadToDocument(payload);
      await this.openSearchService.indexEntity(doc);
      this.logger.log(`Re-indexed entity ${payload.id}: ${payload.name}`);
    } catch (error) {
      this.logger.error(
        `Failed to re-index updated entity ${payload.id}`,
        error instanceof Error ? error.stack : String(error),
      );
    }
  }

  /**
   * Handle entity deletion events from Kafka.
   */
  @MessagePattern('events.entity.deleted')
  async handleEntityDeleted(
    @Payload() payload: { id: string },
  ): Promise<void> {
    this.logger.debug(`Removing deleted entity from index: ${payload.id}`);

    try {
      await this.openSearchService.deleteEntity(payload.id);
      this.logger.log(`Removed entity ${payload.id} from index`);
    } catch (error) {
      this.logger.error(
        `Failed to remove entity ${payload.id} from index`,
        error instanceof Error ? error.stack : String(error),
      );
    }
  }

  /**
   * Map a Kafka event payload to an OpenSearch document.
   */
  private mapPayloadToDocument(payload: EntityEventPayload): EntityDocument {
    const doc: EntityDocument = {
      id: payload.id,
      name: payload.name,
      entityType: payload.entityType,
    };

    if (payload.description) {
      doc.description = payload.description;
    }
    if (payload.source) {
      doc.source = payload.source;
    }
    if (payload.classification) {
      doc.classification = payload.classification;
    }
    if (payload.latitude !== undefined && payload.longitude !== undefined) {
      doc.position = { lat: payload.latitude, lon: payload.longitude };
    }
    if (payload.affiliations) {
      doc.affiliations = payload.affiliations;
    }
    if (payload.metadata) {
      doc.metadata = payload.metadata;
    }
    if (payload.createdAt) {
      doc.createdAt = payload.createdAt;
    }
    if (payload.updatedAt) {
      doc.updatedAt = payload.updatedAt;
    }
    if (payload.lastSeenAt) {
      doc.lastSeenAt = payload.lastSeenAt;
    }

    return doc;
  }
}

interface EntityEventPayload {
  id: string;
  name: string;
  entityType: string;
  description?: string;
  source?: string;
  classification?: string;
  latitude?: number;
  longitude?: number;
  affiliations?: string[];
  metadata?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
  lastSeenAt?: string;
}
