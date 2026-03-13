import {
  Injectable,
  Logger,
  OnModuleInit,
  OnModuleDestroy,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { EventEmitter2 } from '@nestjs/event-emitter';
import WebSocket from 'ws';
import { v4 as uuidv4 } from 'uuid';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPeer } from './entities/federation-peer.entity';
import { SharingPolicyService } from './sharing-policy.service';
import {
  FEDERATION_PROTOCOL_VERSION,
  FEDERATION_PORT_DEFAULT,
  FederationMessage,
  FederationMessageType,
  HandshakePayload,
  PeerConnectionState,
  FederationCloseReason,
} from './federation.types';

interface PeerConnection {
  ws: WebSocket;
  instanceId: string;
  displayName: string;
  classificationLevel: string;
  connectionCeiling: string;
  color: string;
  state: PeerConnectionState;
  lastHeartbeat: number;
  reconnectAttempts: number;
  reconnectTimer?: ReturnType<typeof setTimeout>;
}

const PEER_COLORS = [
  '#f97316', '#a855f7', '#06b6d4', '#eab308', '#ec4899',
  '#14b8a6', '#f43f5e', '#8b5cf6', '#10b981', '#6366f1',
  '#d946ef', '#0ea5e9', '#84cc16', '#e11d48', '#7c3aed',
  '#059669', '#dc2626', '#2563eb', '#ca8a04', '#9333ea',
];

@Injectable()
export class PeerManagerService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(PeerManagerService.name);
  private readonly connections = new Map<string, PeerConnection>();
  private localConfig!: FederationConfig;
  private heartbeatInterval?: ReturnType<typeof setInterval>;

  private static readonly HEARTBEAT_INTERVAL_MS = 10_000;
  private static readonly HEARTBEAT_TIMEOUT_MS = 30_000;
  private static readonly RECONNECT_BASE_MS = 2_000;
  private static readonly RECONNECT_MAX_MS = 60_000;

  constructor(
    private readonly configService: ConfigService,
    @InjectRepository(FederationConfig)
    private readonly configRepo: Repository<FederationConfig>,
    @InjectRepository(FederationPeer)
    private readonly peerRepo: Repository<FederationPeer>,
    private readonly sharingPolicy: SharingPolicyService,
    private readonly eventEmitter: EventEmitter2,
  ) {}

  async onModuleInit(): Promise<void> {
    this.localConfig = await this.getOrCreateConfig();

    this.heartbeatInterval = setInterval(() => {
      const now = Date.now();
      for (const [instanceId, conn] of this.connections) {
        if (conn.state !== 'connected') continue;

        this.sendMessage(conn.ws, {
          type: FederationMessageType.HEARTBEAT,
          sourceInstanceId: this.localConfig.instanceId,
          classificationLevel: this.localConfig.classificationLevel,
          payload: {},
        });

        if (now - conn.lastHeartbeat > PeerManagerService.HEARTBEAT_TIMEOUT_MS) {
          this.logger.warn(`Peer ${instanceId} is stale (no heartbeat for ${PeerManagerService.HEARTBEAT_TIMEOUT_MS}ms)`);
          conn.state = 'stale';
          conn.ws.close(4005, 'heartbeat-timeout');
        }
      }
    }, PeerManagerService.HEARTBEAT_INTERVAL_MS);
  }

  async onModuleDestroy(): Promise<void> {
    if (this.heartbeatInterval) clearInterval(this.heartbeatInterval);
    for (const [, conn] of this.connections) {
      if (conn.reconnectTimer) clearTimeout(conn.reconnectTimer);
      if (conn.ws.readyState === WebSocket.OPEN) {
        this.sendMessage(conn.ws, {
          type: FederationMessageType.HEARTBEAT,
          sourceInstanceId: this.localConfig?.instanceId ?? '',
          classificationLevel: this.localConfig?.classificationLevel ?? '',
          payload: {},
        });
        conn.ws.close(1000, FederationCloseReason.SHUTDOWN);
      }
    }
    this.connections.clear();
  }

  async getOrCreateConfig(): Promise<FederationConfig> {
    let config = await this.configRepo.findOne({ where: {}, order: { createdAt: 'ASC' } });
    if (config) return config;

    config = this.configRepo.create({
      instanceId: uuidv4(),
      displayName: this.configService.get<string>('FEDERATION_DISPLAY_NAME', 'Sentinel'),
      classificationLevel: this.configService.get<string>('FEDERATION_CLASSIFICATION', 'classification-u'),
      federationEnabled: false,
    });

    return this.configRepo.save(config);
  }

  validateHandshake(payload: HandshakePayload): { valid: boolean; reason?: string } {
    if (!payload.instanceId) {
      return { valid: false, reason: 'missing-instance-id' };
    }
    if (payload.protocolVersion !== FEDERATION_PROTOCOL_VERSION) {
      return { valid: false, reason: FederationCloseReason.VERSION_MISMATCH };
    }
    return { valid: true };
  }

  async connectToPeer(url: string): Promise<void> {
    if (!this.localConfig) {
      this.localConfig = await this.getOrCreateConfig();
    }

    this.logger.log(`Connecting to peer at ${url}`);
    const ws = new WebSocket(url);

    ws.on('open', () => {
      this.logger.log(`WebSocket connected to ${url}`);
      const handshake: FederationMessage = {
        type: FederationMessageType.HANDSHAKE,
        sourceInstanceId: this.localConfig.instanceId,
        classificationLevel: this.localConfig.classificationLevel,
        payload: {
          instanceId: this.localConfig.instanceId,
          displayName: this.localConfig.displayName,
          classificationLevel: this.localConfig.classificationLevel,
          protocolVersion: FEDERATION_PROTOCOL_VERSION,
        } as HandshakePayload,
      };
      this.sendMessage(ws, handshake);
    });

    ws.on('message', (data: WebSocket.Data) => {
      this.handleIncomingMessage(ws, url, data);
    });

    ws.on('close', (code: number, reason: Buffer) => {
      const reasonStr = reason.toString();
      this.logger.log(`Peer ${url} disconnected: ${code} ${reasonStr}`);
      this.handlePeerDisconnect(url, reasonStr);
    });

    ws.on('error', (err: Error) => {
      this.logger.error(`WebSocket error for ${url}: ${err.message}`);
    });
  }

  private handleIncomingMessage(ws: WebSocket, url: string, data: WebSocket.Data): void {
    let message: FederationMessage;
    try {
      message = JSON.parse(data.toString()) as FederationMessage;
    } catch {
      this.logger.warn(`Invalid JSON from ${url}`);
      return;
    }

    switch (message.type) {
      case FederationMessageType.HANDSHAKE:
        this.handleHandshakeResponse(ws, url, message.payload as HandshakePayload);
        break;
      case FederationMessageType.HEARTBEAT:
        this.handleHeartbeat(message.sourceInstanceId);
        break;
      case FederationMessageType.ENTITY_BATCH:
      case FederationMessageType.PRESENCE_UPDATE:
      case FederationMessageType.PRESENCE_REMOVE:
        this.eventEmitter.emit(`federation.${message.type}`, message);
        break;
      default:
        this.logger.warn(`Unknown message type from ${url}: ${(message as FederationMessage).type}`);
    }
  }

  private async handleHandshakeResponse(ws: WebSocket, url: string, payload: HandshakePayload): Promise<void> {
    const validation = this.validateHandshake(payload);
    if (!validation.valid) {
      this.logger.warn(`Handshake rejected from ${url}: ${validation.reason}`);
      ws.close(4000, validation.reason);
      return;
    }

    if (this.connections.has(payload.instanceId)) {
      this.logger.warn(`Already connected to ${payload.instanceId}, closing duplicate`);
      ws.close(4001, 'duplicate-connection');
      return;
    }

    const ceiling = this.sharingPolicy.getClassificationCeiling(
      this.localConfig.classificationLevel,
      payload.classificationLevel,
    );

    const conn: PeerConnection = {
      ws,
      instanceId: payload.instanceId,
      displayName: payload.displayName,
      classificationLevel: payload.classificationLevel,
      connectionCeiling: ceiling,
      color: this.assignPeerColor(payload.instanceId),
      state: 'connected',
      lastHeartbeat: Date.now(),
      reconnectAttempts: 0,
    };

    this.connections.set(payload.instanceId, conn);

    await this.peerRepo.upsert(
      {
        instanceId: payload.instanceId,
        displayName: payload.displayName,
        url,
        classificationLevel: payload.classificationLevel,
        status: 'connected',
        lastSeen: new Date(),
        color: this.assignPeerColor(payload.instanceId),
      },
      ['instanceId'],
    );

    this.eventEmitter.emit('federation.peer.connected', {
      instanceId: payload.instanceId,
      displayName: payload.displayName,
      ceiling,
    });

    this.logger.log(`Peer connected: ${payload.displayName} (${payload.instanceId}), ceiling: ${ceiling}`);
  }

  private handleHeartbeat(instanceId: string): void {
    const conn = this.connections.get(instanceId);
    if (conn) {
      conn.lastHeartbeat = Date.now();
      conn.state = 'connected';
    }
  }

  private handlePeerDisconnect(url: string, reason: string): void {
    for (const [instanceId, conn] of this.connections) {
      if (conn.ws.url === url || conn.instanceId === instanceId) {
        conn.state = 'disconnected';
        const attempts = conn.reconnectAttempts;

        this.eventEmitter.emit('federation.peer.disconnected', { instanceId });

        if (reason !== FederationCloseReason.POLICY_VIOLATION &&
            reason !== FederationCloseReason.AUTH_FAILURE) {
          conn.reconnectTimer = this.scheduleReconnect(url, attempts);
        } else {
          this.connections.delete(instanceId);
        }
        break;
      }
    }
  }

  private scheduleReconnect(url: string, attempts: number): ReturnType<typeof setTimeout> {
    const delay = Math.min(
      PeerManagerService.RECONNECT_BASE_MS * Math.pow(2, attempts),
      PeerManagerService.RECONNECT_MAX_MS,
    );
    this.logger.log(`Scheduling reconnect to ${url} in ${delay}ms (attempt ${attempts + 1})`);
    return setTimeout(() => {
      for (const [id, conn] of this.connections) {
        if (conn.state === 'disconnected') {
          this.connections.delete(id);
          break;
        }
      }
      this.connectToPeer(url).catch(err => {
        this.logger.error(`Reconnect to ${url} failed: ${err.message}`);
      });
    }, delay);
  }

  sendMessage(ws: WebSocket, message: FederationMessage): void {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(message));
    }
  }

  broadcastToPeers(message: FederationMessage): void {
    for (const [, conn] of this.connections) {
      if (conn.state === 'connected') {
        this.sendMessage(conn.ws, message);
      }
    }
  }

  async registerIncomingPeer(ws: WebSocket, payload: HandshakePayload, url: string): Promise<void> {
    if (this.connections.has(payload.instanceId)) {
      this.logger.warn(`Already connected to ${payload.instanceId}, closing duplicate`);
      ws.close(4001, 'duplicate-connection');
      return;
    }

    const ceiling = this.sharingPolicy.getClassificationCeiling(
      this.localConfig.classificationLevel,
      payload.classificationLevel,
    );

    const conn: PeerConnection = {
      ws,
      instanceId: payload.instanceId,
      displayName: payload.displayName,
      classificationLevel: payload.classificationLevel,
      connectionCeiling: ceiling,
      color: this.assignPeerColor(payload.instanceId),
      state: 'connected',
      lastHeartbeat: Date.now(),
      reconnectAttempts: 0,
    };

    this.connections.set(payload.instanceId, conn);

    ws.on('message', (data: WebSocket.Data) => {
      let message: FederationMessage;
      try {
        message = JSON.parse(data.toString()) as FederationMessage;
      } catch {
        return;
      }
      if (message.type === FederationMessageType.HEARTBEAT) {
        this.handleHeartbeat(message.sourceInstanceId);
      } else {
        this.eventEmitter.emit(`federation.${message.type}`, message);
      }
    });

    ws.on('close', () => {
      this.handlePeerDisconnect(url, 'peer-closed');
    });

    await this.peerRepo.upsert(
      {
        instanceId: payload.instanceId,
        displayName: payload.displayName,
        url,
        classificationLevel: payload.classificationLevel,
        status: 'connected',
        lastSeen: new Date(),
        color: this.assignPeerColor(payload.instanceId),
      },
      ['instanceId'],
    );

    this.eventEmitter.emit('federation.peer.connected', {
      instanceId: payload.instanceId,
      displayName: payload.displayName,
      ceiling,
    });

    this.logger.log(`Inbound peer registered: ${payload.displayName} (${payload.instanceId}), ceiling: ${ceiling}`);
  }

  getPeerState(instanceId: string): PeerConnectionState {
    return this.connections.get(instanceId)?.state ?? 'disconnected';
  }

  getConnectedPeers(): Array<{ instanceId: string; displayName: string; ceiling: string; color: string }> {
    const result: Array<{ instanceId: string; displayName: string; ceiling: string; color: string }> = [];
    for (const [, conn] of this.connections) {
      if (conn.state === 'connected') {
        result.push({
          instanceId: conn.instanceId,
          displayName: conn.displayName,
          ceiling: conn.connectionCeiling,
          color: conn.color,
        });
      }
    }
    return result;
  }

  getConnectionCeiling(instanceId: string): string | null {
    return this.connections.get(instanceId)?.connectionCeiling ?? null;
  }

  private assignPeerColor(instanceId: string): string {
    let hash = 0;
    for (let i = 0; i < instanceId.length; i++) {
      hash = ((hash << 5) - hash + instanceId.charCodeAt(i)) | 0;
    }
    return PEER_COLORS[Math.abs(hash) % PEER_COLORS.length];
  }
}
