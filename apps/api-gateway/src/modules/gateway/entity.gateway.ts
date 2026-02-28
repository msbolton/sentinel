import {
  WebSocketGateway,
  WebSocketServer,
  SubscribeMessage,
  OnGatewayConnection,
  OnGatewayDisconnect,
  MessageBody,
  ConnectedSocket,
  WsException,
} from '@nestjs/websockets';
import { Logger, UseFilters } from '@nestjs/common';
import { Server, Socket } from 'socket.io';
import { ViewportService, ViewportBounds } from './viewport.service';

/**
 * Payload for entity position updates broadcast to clients.
 */
export interface EntityPositionUpdate {
  entityId: string;
  entityType: string;
  latitude: number;
  longitude: number;
  altitude?: number;
  heading?: number;
  speed?: number;
  classification: string;
  source: string;
  timestamp: string;
  metadata?: Record<string, unknown>;
}

/**
 * Frontend-compatible entity event shape.
 */
export interface EntityEvent {
  type: 'created' | 'updated' | 'deleted';
  entity: {
    id: string;
    entityType: string;
    name?: string;
    description?: string;
    source: string;
    classification: string;
    position?: { latitude: number; longitude: number; altitude?: number };
    heading?: number;
    speedKnots?: number;
    course?: number;
    milStd2525dSymbol?: string;
    metadata: Record<string, unknown>;
    affiliations: string[];
    createdAt: string;
    updatedAt: string;
    lastSeenAt?: string;
  };
  timestamp: string;
}

/**
 * Subscription request for a specific entity's updates.
 */
interface EntitySubscribePayload {
  entityId: string;
}

/**
 * WebSocket gateway for real-time entity position streaming.
 *
 * Clients connect to the /entities namespace, register a viewport bounding box,
 * and receive only entity updates that fall within their visible area. This
 * viewport-based filtering dramatically reduces bandwidth for dense operational
 * environments.
 */
@WebSocketGateway({
  namespace: '/entities',
  cors: {
    origin: '*',
    credentials: true,
  },
  transports: ['websocket', 'polling'],
})
export class EntityGateway
  implements OnGatewayConnection, OnGatewayDisconnect
{
  @WebSocketServer()
  server!: Server;

  private readonly logger = new Logger(EntityGateway.name);

  /**
   * In-memory map of connected clients to their subscribed entity IDs.
   * Supplements the viewport-based filtering for targeted subscriptions.
   */
  private readonly entitySubscriptions = new Map<string, Set<string>>();

  constructor(private readonly viewportService: ViewportService) {}

  /**
   * Called when a client connects. Registers the client with a default
   * global viewport so they receive all updates until they narrow their view.
   */
  async handleConnection(client: Socket): Promise<void> {
    const clientId = client.id;
    this.logger.log(`Client connected: ${clientId}`);

    // Register with a default global viewport
    const defaultViewport: ViewportBounds = {
      north: 90,
      south: -90,
      east: 180,
      west: -180,
    };

    try {
      await this.viewportService.setViewport(clientId, defaultViewport);
      this.entitySubscriptions.set(clientId, new Set());

      client.emit('connection:ack', {
        clientId,
        viewport: defaultViewport,
        timestamp: new Date().toISOString(),
      });
    } catch (error) {
      this.logger.error(
        `Failed to register client ${clientId}: ${error}`,
      );
      client.disconnect(true);
    }
  }

  /**
   * Called when a client disconnects. Cleans up viewport registration
   * and entity subscriptions.
   */
  async handleDisconnect(client: Socket): Promise<void> {
    const clientId = client.id;
    this.logger.log(`Client disconnected: ${clientId}`);

    try {
      await this.viewportService.removeViewport(clientId);
      this.entitySubscriptions.delete(clientId);
    } catch (error) {
      this.logger.error(
        `Failed to cleanup client ${clientId}: ${error}`,
      );
    }
  }

  /**
   * Handles viewport update messages from clients. The client sends their
   * visible map bounds, and only entities within these bounds will be streamed.
   */
  @SubscribeMessage('viewport:update')
  async handleViewportUpdate(
    @ConnectedSocket() client: Socket,
    @MessageBody() viewport: ViewportBounds,
  ): Promise<{ event: string; data: { success: boolean; viewport: ViewportBounds } }> {
    const clientId = client.id;

    // Validate viewport bounds
    if (!this.isValidViewport(viewport)) {
      throw new WsException(
        'Invalid viewport bounds. Expected {north, south, east, west} with valid lat/lng ranges.',
      );
    }

    this.logger.debug(
      `Viewport update from ${clientId}: N=${viewport.north} S=${viewport.south} E=${viewport.east} W=${viewport.west}`,
    );

    await this.viewportService.setViewport(clientId, viewport);

    return {
      event: 'viewport:updated',
      data: { success: true, viewport },
    };
  }

  /**
   * Handles subscription requests for specific entity updates.
   * Subscribed entities bypass viewport filtering - the client always
   * receives updates for explicitly subscribed entities.
   */
  @SubscribeMessage('entity:subscribe')
  handleEntitySubscribe(
    @ConnectedSocket() client: Socket,
    @MessageBody() payload: EntitySubscribePayload,
  ): { event: string; data: { entityId: string; subscribed: boolean } } {
    const clientId = client.id;

    if (!payload.entityId) {
      throw new WsException('entityId is required');
    }

    const subscriptions = this.entitySubscriptions.get(clientId);
    if (subscriptions) {
      subscriptions.add(payload.entityId);
      this.logger.debug(
        `Client ${clientId} subscribed to entity ${payload.entityId}`,
      );
    }

    // Join a Socket.IO room for this entity for efficient targeted broadcasts
    client.join(`entity:${payload.entityId}`);

    return {
      event: 'entity:subscribed',
      data: { entityId: payload.entityId, subscribed: true },
    };
  }

  /**
   * Handles unsubscription from specific entity updates.
   */
  @SubscribeMessage('entity:unsubscribe')
  handleEntityUnsubscribe(
    @ConnectedSocket() client: Socket,
    @MessageBody() payload: EntitySubscribePayload,
  ): { event: string; data: { entityId: string; subscribed: boolean } } {
    const clientId = client.id;

    if (!payload.entityId) {
      throw new WsException('entityId is required');
    }

    const subscriptions = this.entitySubscriptions.get(clientId);
    if (subscriptions) {
      subscriptions.delete(payload.entityId);
    }

    client.leave(`entity:${payload.entityId}`);

    return {
      event: 'entity:unsubscribed',
      data: { entityId: payload.entityId, subscribed: false },
    };
  }

  /**
   * Broadcasts an entity position update to all clients whose viewport
   * contains the entity's position, plus any clients explicitly subscribed
   * to the entity.
   *
   * This is the core viewport-filtering broadcast method called by the
   * Kafka consumer when new entity positions arrive.
   */
  async broadcastEntityUpdate(entity: EntityPositionUpdate, eventType: 'created' | 'updated' = 'updated'): Promise<void> {
    if (!this.server) {
      return;
    }

    // Transform to frontend EntityEvent shape
    const event: EntityEvent = {
      type: eventType,
      entity: {
        id: entity.entityId,
        entityType: entity.entityType,
        source: entity.source,
        classification: entity.classification,
        position: { latitude: entity.latitude, longitude: entity.longitude, altitude: entity.altitude },
        heading: entity.heading,
        speedKnots: entity.speed,
        metadata: entity.metadata ?? {},
        affiliations: [],
        createdAt: entity.timestamp,
        updatedAt: entity.timestamp,
      },
      timestamp: entity.timestamp,
    };

    const eventChannel = `entity:${eventType}`;

    const sockets = await this.server.fetchSockets();
    let sentCount = 0;

    for (const socket of sockets) {
      const clientId = socket.id;
      const shouldSend = await this.shouldSendToClient(clientId, entity);

      if (shouldSend) {
        socket.emit(eventChannel, event);
        sentCount++;
      }
    }

    // Also emit to the entity-specific room for explicit subscribers
    // who may be outside the viewport
    this.server.to(`entity:${entity.entityId}`).emit(eventChannel, event);

    this.logger.verbose(
      `Broadcast entity ${entity.entityId} to ${sentCount} viewport-matched clients`,
    );
  }

  /**
   * Broadcasts a full EntityEvent directly (used when the event is already shaped).
   */
  broadcastEntityEvent(event: EntityEvent): void {
    if (!this.server) return;
    const eventChannel = `entity:${event.type}`;
    this.server.emit(eventChannel, event);
  }

  /**
   * Determines whether a given client should receive an entity update
   * based on viewport containment or explicit subscription.
   */
  private async shouldSendToClient(
    clientId: string,
    entity: EntityPositionUpdate,
  ): Promise<boolean> {
    // Check explicit subscription first (fast path)
    const subscriptions = this.entitySubscriptions.get(clientId);
    if (subscriptions?.has(entity.entityId)) {
      return true;
    }

    // Check viewport containment
    return this.viewportService.isEntityInViewport(
      clientId,
      entity.latitude,
      entity.longitude,
    );
  }

  /**
   * Validates that viewport bounds are within acceptable ranges.
   */
  private isValidViewport(viewport: ViewportBounds): boolean {
    if (
      viewport.north == null ||
      viewport.south == null ||
      viewport.east == null ||
      viewport.west == null
    ) {
      return false;
    }

    if (viewport.north < -90 || viewport.north > 90) return false;
    if (viewport.south < -90 || viewport.south > 90) return false;
    if (viewport.east < -180 || viewport.east > 180) return false;
    if (viewport.west < -180 || viewport.west > 180) return false;
    if (viewport.north <= viewport.south) return false;

    return true;
  }
}
