import { Injectable } from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPolicy } from './entities/federation-policy.entity';

const ROLE_TO_CLASSIFICATION: Record<string, string> = {
  'classification-u': 'UNCLASSIFIED',
  'classification-s': 'SECRET',
  'classification-ts': 'TOP_SECRET',
};

const CLASSIFICATION_ORDER = ['UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET'] as const;

const INSTANCE_CLASSIFICATION_ORDER = ['classification-u', 'classification-s', 'classification-ts'] as const;

interface EntityForPolicy {
  entityType: string;
  classification: string;
  latitude: number;
  longitude: number;
  sourceInstanceId?: string;
}

interface GeoBounds {
  north: number;
  south: number;
  east: number;
  west: number;
}

@Injectable()
export class SharingPolicyService {
  constructor(
    @InjectRepository(FederationConfig)
    private readonly configRepo: Repository<FederationConfig>,
    @InjectRepository(FederationPolicy)
    private readonly policyRepo: Repository<FederationPolicy>,
  ) {}

  getClassificationCeiling(levelA: string, levelB: string): string {
    const indexA = INSTANCE_CLASSIFICATION_ORDER.indexOf(levelA as typeof INSTANCE_CLASSIFICATION_ORDER[number]);
    const indexB = INSTANCE_CLASSIFICATION_ORDER.indexOf(levelB as typeof INSTANCE_CLASSIFICATION_ORDER[number]);
    return INSTANCE_CLASSIFICATION_ORDER[Math.min(indexA, indexB)] ?? 'classification-u';
  }

  isClassificationAllowed(entityClassification: string, ceiling: string): boolean {
    const ceilingAsEnum = ROLE_TO_CLASSIFICATION[ceiling];
    if (!ceilingAsEnum) return false;
    const entityIndex = CLASSIFICATION_ORDER.indexOf(entityClassification as typeof CLASSIFICATION_ORDER[number]);
    const ceilingIndex = CLASSIFICATION_ORDER.indexOf(ceilingAsEnum as typeof CLASSIFICATION_ORDER[number]);
    if (entityIndex === -1 || ceilingIndex === -1) return false;
    return entityIndex <= ceilingIndex;
  }

  isEntityTypeAllowed(entityType: string, allowedTypes: string[]): boolean {
    if (allowedTypes.length === 0) return true;
    return allowedTypes.includes(entityType);
  }

  isInGeoBounds(lat: number, lon: number, bounds: GeoBounds | null): boolean {
    if (!bounds) return true;
    if (lat < bounds.south || lat > bounds.north) return false;

    if (bounds.west > bounds.east) {
      return lon >= bounds.west || lon <= bounds.east;
    }
    return lon >= bounds.west && lon <= bounds.east;
  }

  async shouldShareEntity(
    entity: EntityForPolicy,
    peerInstanceId: string,
    connectionCeiling: string,
  ): Promise<boolean> {
    if (entity.sourceInstanceId) return false;
    if (!this.isClassificationAllowed(entity.classification, connectionCeiling)) return false;

    const policy = await this.policyRepo.findOne({
      where: { peerInstanceId },
    });

    if (policy && !policy.enabled) return false;
    if (policy && !this.isEntityTypeAllowed(entity.entityType, policy.entityTypesAllowed)) return false;
    if (policy && !this.isInGeoBounds(entity.latitude, entity.longitude, policy.geoBounds)) return false;

    return true;
  }

  async getConfig(): Promise<FederationConfig | null> {
    return this.configRepo.findOne({ where: {}, order: { createdAt: 'ASC' } });
  }
}
