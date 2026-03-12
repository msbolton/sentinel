import { Test, TestingModule } from '@nestjs/testing';
import { ConfigService } from '@nestjs/config';
import { getRepositoryToken } from '@nestjs/typeorm';
import { EventEmitter2 } from '@nestjs/event-emitter';
import { PeerManagerService } from './peer-manager.service';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPeer } from './entities/federation-peer.entity';
import { SharingPolicyService } from './sharing-policy.service';
import {
  FEDERATION_PROTOCOL_VERSION,
  FederationMessageType,
  HandshakePayload,
  PeerConnectionState,
} from './federation.types';

describe('PeerManagerService', () => {
  let service: PeerManagerService;

  const mockConfigRepo = {
    findOne: jest.fn(),
    save: jest.fn(),
    create: jest.fn(),
  };

  const mockPeerRepo = {
    findOne: jest.fn(),
    save: jest.fn(),
    upsert: jest.fn(),
    find: jest.fn(),
  };

  const mockSharingPolicy = {
    getClassificationCeiling: jest.fn(),
    getConfig: jest.fn(),
  };

  const mockEventEmitter = {
    emit: jest.fn(),
  };

  const mockConfigService = {
    get: jest.fn((key: string, defaultValue?: unknown) => {
      const values: Record<string, unknown> = {
        FEDERATION_PORT: 3100,
        FEDERATION_CLASSIFICATION: 'classification-u',
        FEDERATION_PSK: 'test-psk',
      };
      return values[key] ?? defaultValue;
    }),
  };

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      providers: [
        PeerManagerService,
        { provide: ConfigService, useValue: mockConfigService },
        { provide: getRepositoryToken(FederationConfig), useValue: mockConfigRepo },
        { provide: getRepositoryToken(FederationPeer), useValue: mockPeerRepo },
        { provide: SharingPolicyService, useValue: mockSharingPolicy },
        { provide: EventEmitter2, useValue: mockEventEmitter },
      ],
    }).compile();

    service = module.get<PeerManagerService>(PeerManagerService);
    jest.clearAllMocks();
  });

  describe('getOrCreateConfig', () => {
    it('should return existing config', async () => {
      const existing = { instanceId: 'abc-123', displayName: 'Alpha' };
      mockConfigRepo.findOne.mockResolvedValue(existing);
      const config = await service.getOrCreateConfig();
      expect(config).toBe(existing);
    });

    it('should create config on first boot', async () => {
      mockConfigRepo.findOne.mockResolvedValue(null);
      mockConfigRepo.create.mockImplementation((data: Record<string, unknown>) => data);
      mockConfigRepo.save.mockImplementation((data: Record<string, unknown>) => Promise.resolve(data));
      const config = await service.getOrCreateConfig();
      expect(config.instanceId).toBeDefined();
      expect(config.classificationLevel).toBe('classification-u');
      expect(mockConfigRepo.save).toHaveBeenCalled();
    });
  });

  describe('validateHandshake', () => {
    it('should accept valid handshake', () => {
      const payload: HandshakePayload = {
        instanceId: 'peer-1',
        displayName: 'Bravo',
        classificationLevel: 'classification-u',
        protocolVersion: FEDERATION_PROTOCOL_VERSION,
      };
      const result = service.validateHandshake(payload);
      expect(result.valid).toBe(true);
    });

    it('should reject mismatched protocol version', () => {
      const payload: HandshakePayload = {
        instanceId: 'peer-1',
        displayName: 'Bravo',
        classificationLevel: 'classification-u',
        protocolVersion: 999,
      };
      const result = service.validateHandshake(payload);
      expect(result.valid).toBe(false);
      expect(result.reason).toBe('version-mismatch');
    });

    it('should reject missing instanceId', () => {
      const payload = {
        displayName: 'Bravo',
        classificationLevel: 'classification-u',
        protocolVersion: FEDERATION_PROTOCOL_VERSION,
      } as HandshakePayload;
      const result = service.validateHandshake(payload);
      expect(result.valid).toBe(false);
    });
  });

  describe('getPeerState', () => {
    it('should return disconnected for unknown peer', () => {
      expect(service.getPeerState('unknown')).toBe('disconnected');
    });
  });

  describe('getConnectedPeers', () => {
    it('should return empty array when no peers connected', () => {
      expect(service.getConnectedPeers()).toEqual([]);
    });
  });
});
