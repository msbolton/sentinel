import { Test, TestingModule } from '@nestjs/testing';
import { ConfigService } from '@nestjs/config';
import { DiscoveryService } from './discovery.service';
import { PeerManagerService } from './peer-manager.service';

describe('DiscoveryService', () => {
  let service: DiscoveryService;

  const mockPeerManager = {
    getOrCreateConfig: jest.fn().mockResolvedValue({
      instanceId: 'local-id',
      displayName: 'Alpha',
      federationEnabled: true,
    }),
    connectToPeer: jest.fn(),
    getPeerState: jest.fn().mockReturnValue('disconnected'),
  };

  const mockConfigService = {
    get: jest.fn((key: string, defaultValue?: unknown) => {
      const values: Record<string, unknown> = {
        FEDERATION_PORT: 3100,
        FEDERATION_SEEDS: '',
        FEDERATION_PSK: 'test-psk',
      };
      return values[key] ?? defaultValue;
    }),
  };

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      providers: [
        DiscoveryService,
        { provide: PeerManagerService, useValue: mockPeerManager },
        { provide: ConfigService, useValue: mockConfigService },
      ],
    }).compile();

    service = module.get<DiscoveryService>(DiscoveryService);
    jest.clearAllMocks();
  });

  it('should be defined', () => {
    expect(service).toBeDefined();
  });

  describe('parseSeedList', () => {
    it('should parse comma-separated URLs', () => {
      const seeds = service.parseSeedList('ws://10.0.1.5:3100,ws://10.0.1.6:3100');
      expect(seeds).toEqual(['ws://10.0.1.5:3100', 'ws://10.0.1.6:3100']);
    });

    it('should return empty array for empty string', () => {
      expect(service.parseSeedList('')).toEqual([]);
    });

    it('should trim whitespace', () => {
      const seeds = service.parseSeedList('  ws://10.0.1.5:3100 , ws://10.0.1.6:3100  ');
      expect(seeds).toEqual(['ws://10.0.1.5:3100', 'ws://10.0.1.6:3100']);
    });
  });

  describe('buildPeerUrl', () => {
    it('should construct WebSocket URL with PSK', () => {
      const url = service.buildPeerUrl('10.0.1.5', 3100);
      expect(url).toBe('ws://10.0.1.5:3100?psk=test-psk');
    });
  });
});
