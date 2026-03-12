import { Test, TestingModule } from '@nestjs/testing';
import { getRepositoryToken } from '@nestjs/typeorm';
import { SharingPolicyService } from './sharing-policy.service';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPolicy } from './entities/federation-policy.entity';

describe('SharingPolicyService', () => {
  let service: SharingPolicyService;

  const mockConfigRepo = {
    findOne: jest.fn(),
  };

  const mockPolicyRepo = {
    findOne: jest.fn(),
  };

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      providers: [
        SharingPolicyService,
        { provide: getRepositoryToken(FederationConfig), useValue: mockConfigRepo },
        { provide: getRepositoryToken(FederationPolicy), useValue: mockPolicyRepo },
      ],
    }).compile();

    service = module.get<SharingPolicyService>(SharingPolicyService);
    jest.clearAllMocks();
  });

  describe('getClassificationCeiling', () => {
    it('should return the lower classification of two levels', () => {
      expect(service.getClassificationCeiling('classification-ts', 'classification-s')).toBe('classification-s');
      expect(service.getClassificationCeiling('classification-u', 'classification-ts')).toBe('classification-u');
      expect(service.getClassificationCeiling('classification-s', 'classification-s')).toBe('classification-s');
    });
  });

  describe('isClassificationAllowed', () => {
    it('should allow entity at or below ceiling (maps entity enum to role name)', () => {
      expect(service.isClassificationAllowed('UNCLASSIFIED', 'classification-s')).toBe(true);
      expect(service.isClassificationAllowed('SECRET', 'classification-s')).toBe(true);
    });

    it('should reject entity above ceiling', () => {
      expect(service.isClassificationAllowed('TOP_SECRET', 'classification-s')).toBe(false);
      expect(service.isClassificationAllowed('SECRET', 'classification-u')).toBe(false);
    });
  });

  describe('isEntityTypeAllowed', () => {
    it('should allow all types when allowlist is empty', () => {
      expect(service.isEntityTypeAllowed('AIRCRAFT', [])).toBe(true);
      expect(service.isEntityTypeAllowed('SHIP', [])).toBe(true);
    });

    it('should filter by allowlist when non-empty', () => {
      const allowed = ['AIRCRAFT', 'SHIP'];
      expect(service.isEntityTypeAllowed('AIRCRAFT', allowed)).toBe(true);
      expect(service.isEntityTypeAllowed('GROUND_VEHICLE', allowed)).toBe(false);
    });
  });

  describe('isInGeoBounds', () => {
    it('should allow all positions when no bounds set', () => {
      expect(service.isInGeoBounds(34.05, -118.25, null)).toBe(true);
    });

    it('should allow position inside bounds', () => {
      const bounds = { north: 40, south: 30, east: -110, west: -120 };
      expect(service.isInGeoBounds(35, -115, bounds)).toBe(true);
    });

    it('should reject position outside bounds', () => {
      const bounds = { north: 40, south: 30, east: -110, west: -120 };
      expect(service.isInGeoBounds(45, -115, bounds)).toBe(false);
      expect(service.isInGeoBounds(35, -100, bounds)).toBe(false);
    });

    it('should handle antimeridian crossing', () => {
      const bounds = { north: 40, south: 30, east: -170, west: 170 };
      expect(service.isInGeoBounds(35, 175, bounds)).toBe(true);
      expect(service.isInGeoBounds(35, -175, bounds)).toBe(true);
      expect(service.isInGeoBounds(35, 0, bounds)).toBe(false);
    });
  });

  describe('shouldShareEntity', () => {
    const entity = {
      entityType: 'AIRCRAFT',
      classification: 'UNCLASSIFIED',
      latitude: 35,
      longitude: -115,
      sourceInstanceId: undefined as string | undefined,
    };

    it('should share a local entity that passes all filters', async () => {
      mockPolicyRepo.findOne.mockResolvedValue(null);
      const result = await service.shouldShareEntity(entity, 'peer-1', 'classification-s');
      expect(result).toBe(true);
    });

    it('should reject federated entities (no re-sharing)', async () => {
      const fedEntity = { ...entity, sourceInstanceId: 'other-instance' };
      const result = await service.shouldShareEntity(fedEntity, 'peer-1', 'classification-s');
      expect(result).toBe(false);
    });

    it('should reject entity above classification ceiling', async () => {
      const tsEntity = { ...entity, classification: 'TOP_SECRET' };
      mockPolicyRepo.findOne.mockResolvedValue(null);
      const result = await service.shouldShareEntity(tsEntity, 'peer-1', 'classification-u');
      expect(result).toBe(false);
    });

    it('should reject entity type not in allowlist', async () => {
      mockPolicyRepo.findOne.mockResolvedValue({
        entityTypesAllowed: ['SHIP'],
        geoBounds: null,
        enabled: true,
      });
      const result = await service.shouldShareEntity(entity, 'peer-1', 'classification-s');
      expect(result).toBe(false);
    });

    it('should reject entity outside geo bounds', async () => {
      mockPolicyRepo.findOne.mockResolvedValue({
        entityTypesAllowed: [],
        geoBounds: { north: 10, south: 0, east: 10, west: 0 },
        enabled: true,
      });
      const result = await service.shouldShareEntity(entity, 'peer-1', 'classification-s');
      expect(result).toBe(false);
    });

    it('should reject when policy is disabled', async () => {
      mockPolicyRepo.findOne.mockResolvedValue({
        entityTypesAllowed: [],
        geoBounds: null,
        enabled: false,
      });
      const result = await service.shouldShareEntity(entity, 'peer-1', 'classification-s');
      expect(result).toBe(false);
    });
  });
});
