import {
  Injectable,
  Logger,
  OnModuleInit,
  OnModuleDestroy,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { Kafka, Consumer, EachMessagePayload } from 'kafkajs';
import { EntityGateway, EntityPositionUpdate } from './entity.gateway';

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
  private readonly kafka: Kafka;

  private static readonly ENTITY_POSITION_TOPIC = 'events.entity.position';
  private static readonly ENTITY_CREATED_TOPIC = 'events.entity.created';
  private static readonly ENTITY_DELETED_TOPIC = 'events.entity.deleted';

  constructor(
    private readonly configService: ConfigService,
    private readonly entityGateway: EntityGateway,
  ) {
    const broker = this.configService.get<string>(
      'KAFKA_BROKER',
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
      this.logger.log('Kafka consumer connected');

      await this.consumer.subscribe({
        topics: [
          KafkaConsumerService.ENTITY_POSITION_TOPIC,
          KafkaConsumerService.ENTITY_CREATED_TOPIC,
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
        `Subscribed to topics: ${KafkaConsumerService.ENTITY_POSITION_TOPIC}, ${KafkaConsumerService.ENTITY_CREATED_TOPIC}, ${KafkaConsumerService.ENTITY_DELETED_TOPIC}`,
      );
    } catch (error) {
      this.logger.error(`Failed to initialize Kafka consumer: ${error}`);
      // Don't throw - allow the service to start without Kafka for development
    }
  }

  async onModuleDestroy(): Promise<void> {
    try {
      await this.consumer?.disconnect();
      this.logger.log('Kafka consumer disconnected');
    } catch (error) {
      this.logger.error(`Error disconnecting Kafka consumer: ${error}`);
    }
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
   * Handles entity position update events. Deserializes the message
   * and forwards to the gateway for viewport-filtered broadcast.
   */
  private async handlePositionEvent(value: Buffer): Promise<void> {
    const raw = this.deserializeMessage<RawEntityPositionEvent>(value);
    if (!raw) return;

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
    };

    await this.entityGateway.broadcastEntityUpdate(update);
  }

  /**
   * Handles entity creation events - broadcasts to all clients as a
   * new entity appearing on the map.
   */
  private async handleEntityCreatedEvent(value: Buffer): Promise<void> {
    const raw = this.deserializeMessage<RawEntityPositionEvent>(value);
    if (!raw) return;

    const update: EntityPositionUpdate = {
      entityId: raw.entity_id,
      entityType: raw.entity_type,
      latitude: raw.latitude,
      longitude: raw.longitude,
      altitude: raw.altitude_meters,
      classification: raw.classification,
      source: raw.source,
      timestamp: raw.timestamp,
      metadata: { ...raw.metadata, event: 'created' },
    };

    await this.entityGateway.broadcastEntityUpdate(update);
  }

  /**
   * Handles entity deletion events - broadcasts removal to all clients.
   */
  private async handleEntityDeletedEvent(value: Buffer): Promise<void> {
    const raw = this.deserializeMessage<{ entity_id: string }>(value);
    if (!raw) return;

    // Broadcast a deletion event to the entity-specific room
    // The gateway server emits directly since deleted entities have no position
    if (this.entityGateway.server) {
      this.entityGateway.server
        .to(`entity:${raw.entity_id}`)
        .emit('entity:deleted', { entityId: raw.entity_id });
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
      this.logger.warn(
        'Failed to deserialize message as JSON. Protobuf deserialization not yet implemented.',
      );
      // TODO: Add protobuf deserialization using @sentinel/proto-gen
      // const decoded = EntityPositionEvent.decode(value);
      return null;
    }
  }
}
