import { Test, TestingModule } from '@nestjs/testing';
import { ConfigService } from '@nestjs/config';
import { FederationGateway } from './federation.gateway';
import { PeerManagerService } from './peer-manager.service';
import { SharingPolicyService } from './sharing-policy.service';
import { FEDERATION_PORT_DEFAULT } from './federation.types';

describe('FederationGateway', () => {
  let gateway: FederationGateway;

  const mockPeerManager = {
    getOrCreateConfig: jest.fn().mockResolvedValue({
      instanceId: 'local-id',
      displayName: 'Alpha',
      classificationLevel: 'classification-u',
      federationEnabled: true,
    }),
    validateHandshake: jest.fn().mockReturnValue({ valid: true }),
    sendMessage: jest.fn(),
    handleIncomingPeerConnection: jest.fn(),
  };

  const mockSharingPolicy = {
    getConfig: jest.fn().mockResolvedValue({ federationEnabled: true }),
  };

  const mockConfigService = {
    get: jest.fn((key: string, defaultValue?: unknown) => {
      if (key === 'FEDERATION_PORT') return FEDERATION_PORT_DEFAULT;
      if (key === 'FEDERATION_PSK') return 'test-psk';
      return defaultValue;
    }),
  };

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      providers: [
        FederationGateway,
        { provide: PeerManagerService, useValue: mockPeerManager },
        { provide: SharingPolicyService, useValue: mockSharingPolicy },
        { provide: ConfigService, useValue: mockConfigService },
      ],
    }).compile();

    gateway = module.get<FederationGateway>(FederationGateway);
    jest.clearAllMocks();
  });

  it('should be defined', () => {
    expect(gateway).toBeDefined();
  });

  describe('getPort', () => {
    it('should return configured federation port', () => {
      expect(gateway.getPort()).toBe(FEDERATION_PORT_DEFAULT);
    });
  });

  describe('verifyPsk', () => {
    it('should accept matching PSK', () => {
      expect(gateway.verifyPsk('test-psk')).toBe(true);
    });

    it('should reject wrong PSK', () => {
      expect(gateway.verifyPsk('wrong')).toBe(false);
    });

    it('should reject when no PSK configured', () => {
      mockConfigService.get.mockReturnValue(undefined);
      const gw = new FederationGateway(
        { get: () => undefined } as unknown as ConfigService,
        mockPeerManager as unknown as PeerManagerService,
        mockSharingPolicy as unknown as SharingPolicyService,
      );
      expect(gw.verifyPsk('anything')).toBe(false);
    });
  });
});
