import {
  Injectable,
  Logger,
  OnModuleInit,
  OnModuleDestroy,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import WebSocket, { WebSocketServer } from 'ws';
import { IncomingMessage } from 'http';
import { PeerManagerService } from './peer-manager.service';
import { SharingPolicyService } from './sharing-policy.service';
import {
  FEDERATION_PORT_DEFAULT,
  FEDERATION_PROTOCOL_VERSION,
  FederationMessage,
  FederationMessageType,
  HandshakePayload,
  FederationCloseReason,
} from './federation.types';

@Injectable()
export class FederationGateway implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(FederationGateway.name);
  private wss?: WebSocketServer;
  private readonly port: number;
  private readonly psk: string | undefined;

  constructor(
    private readonly configService: ConfigService,
    private readonly peerManager: PeerManagerService,
    private readonly sharingPolicy: SharingPolicyService,
  ) {
    this.port = this.configService.get<number>('FEDERATION_PORT', FEDERATION_PORT_DEFAULT);
    this.psk = this.configService.get<string>('FEDERATION_PSK');
  }

  async onModuleInit(): Promise<void> {
    const config = await this.peerManager.getOrCreateConfig();
    if (!config.federationEnabled) {
      this.logger.log('Federation is disabled — not starting WebSocket server');
      return;
    }

    this.wss = new WebSocketServer({ port: this.port });

    this.wss.on('connection', (ws: WebSocket, req: IncomingMessage) => {
      this.handleConnection(ws, req);
    });

    this.wss.on('error', (err: Error) => {
      this.logger.error(`Federation WebSocket server error: ${err.message}`);
    });

    this.logger.log(`Federation WebSocket server listening on port ${this.port}`);
  }

  async onModuleDestroy(): Promise<void> {
    if (this.wss) {
      this.wss.close();
      this.logger.log('Federation WebSocket server stopped');
    }
  }

  getPort(): number {
    return this.port;
  }

  verifyPsk(token: string): boolean {
    if (!this.psk) return false;
    return token === this.psk;
  }

  private handleConnection(ws: WebSocket, req: IncomingMessage): void {
    const remoteAddr = req.socket.remoteAddress ?? 'unknown';
    this.logger.log(`Incoming federation connection from ${remoteAddr}`);

    const url = new URL(req.url ?? '', `http://${req.headers.host}`);
    const pskToken = url.searchParams.get('psk');

    if (this.psk && !this.verifyPsk(pskToken ?? '')) {
      this.logger.warn(`Auth failed for ${remoteAddr}: invalid PSK`);
      ws.close(4003, FederationCloseReason.AUTH_FAILURE);
      return;
    }

    const handshakeTimeout = setTimeout(() => {
      this.logger.warn(`Handshake timeout for ${remoteAddr}`);
      ws.close(4004, 'handshake-timeout');
    }, 10_000);

    ws.on('message', (data: WebSocket.Data) => {
      let message: FederationMessage;
      try {
        message = JSON.parse(data.toString()) as FederationMessage;
      } catch {
        this.logger.warn(`Invalid JSON from ${remoteAddr}`);
        return;
      }

      if (message.type === FederationMessageType.HANDSHAKE) {
        clearTimeout(handshakeTimeout);
        this.handleIncomingHandshake(ws, remoteAddr, message.payload as HandshakePayload);
      }
    });

    ws.on('close', () => {
      clearTimeout(handshakeTimeout);
    });

    ws.on('error', (err: Error) => {
      clearTimeout(handshakeTimeout);
      this.logger.error(`Connection error from ${remoteAddr}: ${err.message}`);
    });
  }

  private async handleIncomingHandshake(
    ws: WebSocket,
    remoteAddr: string,
    payload: HandshakePayload,
  ): Promise<void> {
    const validation = this.peerManager.validateHandshake(payload);
    if (!validation.valid) {
      this.logger.warn(`Handshake rejected from ${remoteAddr}: ${validation.reason}`);
      ws.close(4000, validation.reason);
      return;
    }

    const localConfig = await this.peerManager.getOrCreateConfig();
    this.peerManager.sendMessage(ws, {
      type: FederationMessageType.HANDSHAKE,
      sourceInstanceId: localConfig.instanceId,
      classificationLevel: localConfig.classificationLevel,
      payload: {
        instanceId: localConfig.instanceId,
        displayName: localConfig.displayName,
        classificationLevel: localConfig.classificationLevel,
        protocolVersion: FEDERATION_PROTOCOL_VERSION,
      } as HandshakePayload,
    });

    const peerUrl = `ws://${remoteAddr}:${this.port}`;
    await this.peerManager.registerIncomingPeer(ws, payload, peerUrl);
    this.logger.log(`Handshake accepted from ${payload.displayName} (${payload.instanceId}) at ${remoteAddr}`);
  }
}
