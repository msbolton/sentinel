import { Test, TestingModule } from '@nestjs/testing';
import { HttpException, HttpStatus } from '@nestjs/common';
import { getRepositoryToken } from '@nestjs/typeorm';
import { FederationController } from './federation.controller';
import { PeerManagerService } from './peer-manager.service';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPeer } from './entities/federation-peer.entity';
import { FederationPolicy } from './entities/federation-policy.entity';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';

describe('FederationController', () => {
  let controller: FederationController;

  const mockPeerManager = {
    getOrCreateConfig: jest.fn(),
    getConnectedPeers: jest.fn(),
  };

  const mockConfigRepo = {
    findOne: jest.fn(),
    save: jest.fn(),
  };

  const mockPeerRepo = {
    find: jest.fn(),
    findOne: jest.fn(),
    save: jest.fn(),
    delete: jest.fn(),
  };

  const mockPolicyRepo = {
    find: jest.fn(),
    findOne: jest.fn(),
    save: jest.fn(),
    create: jest.fn(),
    delete: jest.fn(),
  };

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      controllers: [FederationController],
      providers: [
        { provide: PeerManagerService, useValue: mockPeerManager },
        { provide: getRepositoryToken(FederationConfig), useValue: mockConfigRepo },
        { provide: getRepositoryToken(FederationPeer), useValue: mockPeerRepo },
        { provide: getRepositoryToken(FederationPolicy), useValue: mockPolicyRepo },
      ],
    })
      .overrideGuard(JwtAuthGuard)
      .useValue({ canActivate: () => true })
      .compile();

    controller = module.get<FederationController>(FederationController);
    jest.clearAllMocks();
  });

  describe('GET /federation/config', () => {
    it('should return federation config', async () => {
      const config = { instanceId: 'abc', displayName: 'Alpha', federationEnabled: true };
      mockPeerManager.getOrCreateConfig.mockResolvedValue(config);
      const result = await controller.getConfig();
      expect(result).toBe(config);
    });
  });

  describe('PUT /federation/config', () => {
    it('should update display name', async () => {
      const existing = { instanceId: 'abc', displayName: 'Alpha', federationEnabled: false };
      mockPeerManager.getOrCreateConfig.mockResolvedValue(existing);
      mockConfigRepo.save.mockResolvedValue({ ...existing, displayName: 'Bravo' });
      const result = await controller.updateConfig({ displayName: 'Bravo' });
      expect(result.displayName).toBe('Bravo');
    });
  });

  describe('GET /federation/peers', () => {
    it('should return all known peers', async () => {
      const peers = [{ instanceId: 'p1', displayName: 'Bravo' }];
      mockPeerRepo.find.mockResolvedValue(peers);
      const result = await controller.getPeers();
      expect(result).toBe(peers);
    });
  });

  describe('GET /federation/status', () => {
    it('should return connected peers with status', async () => {
      mockPeerManager.getConnectedPeers.mockReturnValue([
        { instanceId: 'p1', displayName: 'Bravo', ceiling: 'classification-u' },
      ]);
      const result = await controller.getStatus();
      expect(result.connectedPeers).toHaveLength(1);
    });
  });

  describe('POST /federation/peers', () => {
    it('should add a seed peer', async () => {
      const peer = { url: 'ws://10.0.1.5:3100', displayName: 'Charlie' };
      mockPeerRepo.save.mockResolvedValue({ ...peer, isSeed: true });
      const result = await controller.addSeedPeer(peer);
      expect(result.isSeed).toBe(true);
    });
  });

  describe('PUT /federation/policies/:peerInstanceId', () => {
    it('should create policy if none exists', async () => {
      mockPolicyRepo.findOne.mockResolvedValue(null);
      mockPolicyRepo.create.mockImplementation((d: Record<string, unknown>) => d);
      mockPolicyRepo.save.mockImplementation((d: Record<string, unknown>) => Promise.resolve(d));

      const result = await controller.updatePolicy('peer-1', {
        entityTypesAllowed: ['AIRCRAFT'],
        enabled: true,
      });
      expect(result.entityTypesAllowed).toEqual(['AIRCRAFT']);
    });

    it('should update existing policy', async () => {
      const existing = { id: 'pol-1', peerInstanceId: 'peer-1', entityTypesAllowed: [], enabled: true };
      mockPolicyRepo.findOne.mockResolvedValue(existing);
      mockPolicyRepo.save.mockImplementation((d: Record<string, unknown>) => Promise.resolve(d));

      const result = await controller.updatePolicy('peer-1', {
        entityTypesAllowed: ['SHIP'],
        enabled: false,
      });
      expect(result.entityTypesAllowed).toEqual(['SHIP']);
      expect(result.enabled).toBe(false);
    });
  });

  describe('DELETE /federation/peers/:instanceId', () => {
    it('should delete a peer and its policy', async () => {
      mockPeerRepo.delete.mockResolvedValue({ affected: 1 });
      mockPolicyRepo.delete.mockResolvedValue({ affected: 1 });
      await controller.removePeer('peer-1');
      expect(mockPeerRepo.delete).toHaveBeenCalledWith({ instanceId: 'peer-1' });
      expect(mockPolicyRepo.delete).toHaveBeenCalledWith({ peerInstanceId: 'peer-1' });
    });
  });
});
