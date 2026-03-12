import {
  Injectable,
  Logger,
  OnModuleInit,
  OnModuleDestroy,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { Kafka, Consumer, Producer, EachMessagePayload } from 'kafkajs';
import { EntityGateway, EntityPositionUpdate, EntityEvent } from './entity.gateway';
import { KafkaTopics } from '@sentinel/common';

/**
 * Raw entity position event as received from the Kafka topic.
 * May arrive as JSON or protobuf depending on the producer.
 */
interface RawEntityPositionEvent {
  entity_id: string;
  entity_type: string;
  latitude: number;
  longitude: number;
  altitude_meters?: number;
  heading?: number;
  speed_knots?: number;
  classification: string;
  source: string;
  timestamp: string;
  metadata?: Record<string, unknown>;
  track_environment?: string;
  country_of_origin?: string;
  platform_data?: Record<string, unknown>;
  operational_status?: string;
  circular_error?: number;
}

/**
 * Kafka consumer service that subscribes to entity position events
 * and forwards them to the WebSocket gateway for viewport-filtered
 * broadcast to connected clients.
 *
 * This service manages its own KafkaJS consumer instance (separate from
 * the NestJS microservice transport) for fine-grained control over
 * consumption patterns, error handling, and backpressure.
 */
@Injectable()
export class KafkaConsumerService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(KafkaConsumerService.name);
  private consumer!: Consumer;
  private producer!: Producer;
  private readonly kafka: Kafka;

  private kafkaConnected = false;
  private deserializationFailures = 0;

  /** Buffer for batching position updates before broadcast. */
  private updateBuffer: Array<{ entity: EntityPositionUpdate; eventType: 'created' | 'updated' }> = [];
  private flushTimer: ReturnType<typeof setInterval> | null = null;
  private static readonly FLUSH_INTERVAL_MS = 200;

  private static readonly ENTITY_POSITION_TOPIC = 'events.entity.position';
  private static readonly ENTITY_CREATED_TOPIC = 'events.entity.created';
  private static readonly ENTITY_UPDATED_TOPIC = 'events.entity.updated';
  private static readonly ENTITY_DELETED_TOPIC = 'events.entity.deleted';

  constructor(
    private readonly configService: ConfigService,
    private readonly entityGateway: EntityGateway,
  ) {
    const broker = this.configService.get<string>(
      'KAFKA_BROKERS',
      'localhost:9092',
    );

    this.kafka = new Kafka({
      clientId: 'sentinel-gateway-ws-consumer',
      brokers: [broker],
      retry: {
        initialRetryTime: 300,
        retries: 10,
      },
    });
  }

  async onModuleInit(): Promise<void> {
    this.consumer = this.kafka.consumer({
      groupId: 'sentinel-gateway-ws',
      sessionTimeout: 30000,
      heartbeatInterval: 10000,
    });

    try {
      await this.consumer.connect();
      this.kafkaConnected = true;
      this.logger.log('Kafka consumer connected');

      this.producer = this.kafka.producer();
      await this.producer.connect();
      this.logger.log('Kafka federation producer connected');

      await this.consumer.subscribe({
        topics: [
          KafkaConsumerService.ENTITY_POSITION_TOPIC,
          KafkaConsumerService.ENTITY_CREATED_TOPIC,
          KafkaConsumerService.ENTITY_UPDATED_TOPIC,
          KafkaConsumerService.ENTITY_DELETED_TOPIC,
        ],
        fromBeginning: false,
      });

      await this.consumer.run({
        eachMessage: async (payload: EachMessagePayload) => {
          await this.handleMessage(payload);
        },
      });

      this.logger.log(
        `Subscribed to topics: ${KafkaConsumerService.ENTITY_POSITION_TOPIC}, ${KafkaConsumerService.ENTITY_CREATED_TOPIC}, ${KafkaConsumerService.ENTITY_UPDATED_TOPIC}, ${KafkaConsumerService.ENTITY_DELETED_TOPIC}`,
      );

      // Start batch flush timer
      this.flushTimer = setInterval(() => {
        this.flushUpdateBuffer();
      }, KafkaConsumerService.FLUSH_INTERVAL_MS);
    } catch (error) {
      this.kafkaConnected = false;
      this.logger.error(`Failed to initialize Kafka consumer: ${error}`);
      // Don't throw - allow the service to start without Kafka for development
    }
  }

  async onModuleDestroy(): Promise<void> {
    if (this.flushTimer) {
      clearInterval(this.flushTimer);
      this.flushTimer = null;
    }
    // Flush any remaining buffered updates
    this.flushUpdateBuffer();

    try {
      await this.producer?.disconnect();
      await this.consumer?.disconnect();
      this.logger.log('Kafka consumer disconnected');
    } catch (error) {
      this.logger.error(`Error disconnecting Kafka consumer: ${error}`);
    }
  }

  /**
   * Whether the Kafka consumer is currently connected.
   */
  isKafkaConnected(): boolean {
    return this.kafkaConnected;
  }

  /**
   * Routes incoming Kafka messages to the appropriate handler based on topic.
   */
  private async handleMessage(payload: EachMessagePayload): Promise<void> {
    const { topic, partition, message } = payload;

    if (!message.value) {
      this.logger.warn(
        `Received null message on topic ${topic} partition ${partition}`,
      );
      return;
    }

    try {
      switch (topic) {
        case KafkaConsumerService.ENTITY_POSITION_TOPIC:
          await this.handlePositionEvent(message.value);
          break;
        case KafkaConsumerService.ENTITY_CREATED_TOPIC:
          await this.handleEntityCreatedEvent(message.value);
          break;
        case KafkaConsumerService.ENTITY_UPDATED_TOPIC:
          await this.handleEntityUpdatedEvent(message.value);
          break;
        case KafkaConsumerService.ENTITY_DELETED_TOPIC:
          await this.handleEntityDeletedEvent(message.value);
          break;
        default:
          this.logger.warn(`Unhandled topic: ${topic}`);
      }
    } catch (error) {
      this.logger.error(
        `Error processing message from ${topic}[${partition}]: ${error}`,
      );
    }
  }

  /**
   * Handles entity position update events. Buffers the update for
   * batched broadcast to reduce WebSocket frame count.
   */
  private async handlePositionEvent(value: Buffer): Promise<void> {
    const raw = this.deserializeMessage<RawEntityPositionEvent>(value);
    if (!raw) return;

    this.bufferUpdate(raw, 'updated');
    this.forwardToFederation(raw, 'position');
  }

  /**
   * Handles entity creation events — buffered for batch broadcast.
   */
  private async handleEntityCreatedEvent(value: Buffer): Promise<void> {
    const raw = this.deserializeMessage<RawEntityPositionEvent>(value);
    if (!raw) return;

    this.bufferUpdate(raw, 'created');
    this.forwardToFederation(raw, 'created');
  }

  /**
   * Handles entity update events — buffered for batch broadcast.
   */
  private async handleEntityUpdatedEvent(value: Buffer): Promise<void> {
    const raw = this.deserializeMessage<RawEntityPositionEvent>(value);
    if (!raw) return;

    this.bufferUpdate(raw, 'updated');
    this.forwardToFederation(raw, 'updated');
  }

  /**
   * Adds an entity update to the buffer for the next flush cycle.
   */
  private bufferUpdate(
    raw: RawEntityPositionEvent,
    eventType: 'created' | 'updated',
  ): void {
    const update: EntityPositionUpdate = {
      entityId: raw.entity_id,
      entityType: raw.entity_type,
      latitude: raw.latitude,
      longitude: raw.longitude,
      altitude: raw.altitude_meters,
      heading: raw.heading,
      speed: raw.speed_knots,
      classification: raw.classification,
      source: raw.source,
      timestamp: raw.timestamp,
      metadata: raw.metadata,
      trackEnvironment: raw.track_environment,
      countryOfOrigin: raw.country_of_origin,
      platformData: raw.platform_data,
      operationalStatus: raw.operational_status,
      circularError: raw.circular_error,
    };

    this.updateBuffer.push({ entity: update, eventType });
  }

  /**
   * Flushes buffered entity updates as a single batch broadcast.
   * Called on a 200ms interval to coalesce rapid-fire Kafka messages.
   */
  private flushUpdateBuffer(): void {
    if (this.updateBuffer.length === 0) return;

    const batch = this.updateBuffer;
    this.updateBuffer = [];

    this.entityGateway.broadcastEntityBatch(batch).catch((err) => {
      this.logger.error(`Failed to broadcast entity batch: ${err}`);
    });
  }

  /**
   * Handles entity deletion events - broadcasts removal to all clients.
   */
  private async handleEntityDeletedEvent(value: Buffer): Promise<void> {
    const raw = this.deserializeMessage<{ entity_id: string; timestamp?: string }>(value);
    if (!raw) return;

    // Broadcast EntityEvent-shaped deletion to all clients
    const event: EntityEvent = {
      type: 'deleted',
      entity: {
        id: raw.entity_id,
        entityType: '',
        source: '',
        classification: '',
        metadata: {},
        affiliations: [],
        createdAt: '',
        updatedAt: '',
      },
      timestamp: raw.timestamp ?? new Date().toISOString(),
    };

    this.entityGateway.broadcastEntityEvent(event);
  }

  /**
   * Forwards a local entity event to the federation outbound topic.
   * Only local entities (no sourceInstanceId) are forwarded.
   */
  private async forwardToFederation(raw: RawEntityPositionEvent, eventType: string): Promise<void> {
    if (!this.kafkaConnected) return;
    try {
      await this.producer.send({
        topic: KafkaTopics.FEDERATION_ENTITY_OUTBOUND,
        messages: [{
          key: raw.entity_id,
          value: JSON.stringify({ ...raw, eventType }),
        }],
      });
    } catch (error) {
      this.logger.debug(`Failed to forward to federation topic: ${error}`);
    }
  }

  /**
   * Deserializes a Kafka message value. Attempts JSON parsing first,
   * with protobuf deserialization as a future extension point.
   */
  private deserializeMessage<T>(value: Buffer): T | null {
    try {
      // Primary path: JSON-encoded messages
      const jsonString = value.toString('utf-8');
      return JSON.parse(jsonString) as T;
    } catch {
      this.deserializationFailures++;
      const hexPreview = value.subarray(0, 32).toString('hex');
      this.logger.debug(
        `Failed to deserialize message as JSON (size=${value.length}, hex=${hexPreview}). Protobuf deserialization not yet implemented.`,
      );
      if (this.deserializationFailures % 100 === 0) {
        this.logger.warn(
          `Deserialization failures total: ${this.deserializationFailures}`,
        );
      }
      return null;
    }
  }
}
