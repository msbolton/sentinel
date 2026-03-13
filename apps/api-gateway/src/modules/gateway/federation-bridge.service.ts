import { Injectable, Logger } from '@nestjs/common';
import { OnEvent } from '@nestjs/event-emitter';
import { EntityGateway, EntityPositionUpdate } from './entity.gateway';
import { PeerManagerService } from '../federation/peer-manager.service';
import {
  FederationMessage,
  EntityBatchPayload,
  PresenceUpdatePayload,
  FederatedEntity,
} from '../federation/federation.types';

@Injectable()
export class FederationBridgeService {
  private readonly logger = new Logger(FederationBridgeService.name);

  constructor(
    private readonly entityGateway: EntityGateway,
    private readonly peerManager: PeerManagerService,
  ) {}

  @OnEvent('federation.fed:entity:batch')
  async handleEntityBatch(message: FederationMessage): Promise<void> {
    const payload = message.payload as EntityBatchPayload;
    if (!payload.entities || payload.entities.length === 0) return;

    const updates = payload.entities.map((e: FederatedEntity) => ({
      entity: {
        entityId: e.entityId,
        entityType: e.entityType,
        latitude: e.latitude,
        longitude: e.longitude,
        altitude: e.altitude,
        heading: e.heading,
        speed: e.speed,
        classification: e.classification,
        source: e.source,
        timestamp: e.timestamp,
        metadata: e.metadata,
        sourceInstanceId: e.sourceInstanceId,
        sourceInstanceName: e.sourceInstanceName,
      } as any as EntityPositionUpdate,
      eventType: 'updated' as const,
    }));

    await this.entityGateway.broadcastEntityBatch(updates);
    this.logger.verbose(
      `Bridged ${payload.entities.length} federated entities from ${message.sourceInstanceId}`,
    );
  }

  @OnEvent('federation.fed:presence:update')
  handlePresenceUpdate(message: FederationMessage): void {
    const payload = message.payload as PresenceUpdatePayload;
    if (!payload.users || payload.users.length === 0) return;

    const peer = this.peerManager
      .getConnectedPeers()
      .find(p => p.instanceId === message.sourceInstanceId);

    const enrichedUsers = payload.users.map(u => ({
      ...u,
      instanceId: message.sourceInstanceId,
      instanceName: peer?.displayName ?? message.sourceInstanceId,
      color: peer?.color ?? '#888888',
    }));

    this.entityGateway.server.emit('federation:presence', { users: enrichedUsers });
  }

  @OnEvent('federation.peer.connected')
  handlePeerConnected(): void {
    this.broadcastFederationStatus();
  }

  @OnEvent('federation.peer.disconnected')
  handlePeerDisconnected(): void {
    this.broadcastFederationStatus();
  }

  private broadcastFederationStatus(): void {
    const connectedPeers = this.peerManager.getConnectedPeers();
    const peers = connectedPeers.map(p => ({
      instanceId: p.instanceId,
      displayName: p.displayName,
      status: 'connected' as const,
      color: p.color,
      entityCount: 0, // TODO: track per-peer entity counts in a future iteration
      userCount: 0,   // TODO: track per-peer user counts in a future iteration
    }));
    this.entityGateway.server.emit('federation:status', { peers });
  }
}
