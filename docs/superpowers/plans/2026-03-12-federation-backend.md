# Federation Backend Core — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the backend Federation Module — database schema, instance identity, sharing policy, peer-to-peer WebSocket connections, discovery, wire protocol, and admin REST endpoints — so that two Sentinel instances can discover each other, authenticate, and stream entities + presence data bidirectionally.

**Architecture:** New NestJS module (`FederationModule`) in the API Gateway with six services: TypeORM entities for persistence, a sharing policy service for classification + allowlist filtering, a peer manager for WebSocket connections + heartbeats, a discovery service for mDNS + seed list, a WebSocket gateway for incoming peer connections, and a REST controller for admin configuration. The Kafka Consumer is modified to forward local entity events to the federation outbound topic.

**Tech Stack:** NestJS, TypeORM (PostgreSQL), KafkaJS, ws (raw WebSocket), multicast-dns (mDNS), ioredis, Jest

**Spec:** `docs/superpowers/specs/2026-03-12-federation-design.md`

---

## Chunk 1: Database Schema, Kafka Topic, and Sharing Policy

### Task 1: Add Federation Kafka Topic

**Files:**
- Modify: `libs/common/src/kafka-topics.ts`

- [ ] **Step 1: Add the federation topic and consumer group**

In `libs/common/src/kafka-topics.ts`, add to `KafkaTopics`:

```typescript
  // Entity deletion (used by kafka-consumer but was missing from centralized constants)
  ENTITY_DELETED: 'events.entity.deleted',

  // Federation events
  FEDERATION_ENTITY_OUTBOUND: 'federation.entity.outbound',
```

And add to `KafkaConsumerGroups`:

```typescript
  FEDERATION: 'sentinel-federation',
```

- [ ] **Step 2: Verify the shared lib builds**

Run: `npx nx build common`
Expected: BUILD SUCCESSFUL

- [ ] **Step 3: Commit**

```bash
git add libs/common/src/kafka-topics.ts
git commit -m "feat(federation): add federation Kafka topic and consumer group"
```

---

### Task 2: Create TypeORM Entities

**Files:**
- Create: `apps/api-gateway/src/modules/federation/entities/federation-config.entity.ts`
- Create: `apps/api-gateway/src/modules/federation/entities/federation-peer.entity.ts`
- Create: `apps/api-gateway/src/modules/federation/entities/federation-policy.entity.ts`
- Create: `apps/api-gateway/src/modules/federation/entities/index.ts`

- [ ] **Step 1: Write tests for entity instantiation**

Create `apps/api-gateway/src/modules/federation/entities/federation-entities.spec.ts`:

```typescript
import { FederationConfig } from './federation-config.entity';
import { FederationPeer } from './federation-peer.entity';
import { FederationPolicy } from './federation-policy.entity';

describe('Federation Entities', () => {
  describe('FederationConfig', () => {
    it('should create an instance with defaults', () => {
      const config = new FederationConfig();
      expect(config).toBeDefined();
      expect(config.federationEnabled).toBe(false);
    });
  });

  describe('FederationPeer', () => {
    it('should create an instance', () => {
      const peer = new FederationPeer();
      expect(peer).toBeDefined();
    });
  });

  describe('FederationPolicy', () => {
    it('should create an instance with defaults', () => {
      const policy = new FederationPolicy();
      expect(policy).toBeDefined();
      expect(policy.enabled).toBe(true);
      expect(policy.entityTypesAllowed).toEqual([]);
    });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test api-gateway --testFile=src/modules/federation/entities/federation-entities.spec.ts`
Expected: FAIL — modules not found

- [ ] **Step 3: Create FederationConfig entity**

Create `apps/api-gateway/src/modules/federation/entities/federation-config.entity.ts`:

```typescript
import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  CreateDateColumn,
  UpdateDateColumn,
} from 'typeorm';

/**
 * Singleton table storing this instance's federation identity and settings.
 * Generated on first boot; the instance_id is stable across restarts.
 */
@Entity('federation_config', { schema: 'sentinel' })
export class FederationConfig {
  @PrimaryGeneratedColumn('uuid')
  id!: string;

  /** Stable UUID identifying this instance in federation messages. */
  @Column({ type: 'uuid', name: 'instance_id' })
  instanceId!: string;

  /** Human-readable name shown to peers (admin-configurable). */
  @Column({ type: 'varchar', length: 255, name: 'display_name', default: 'Sentinel' })
  displayName!: string;

  /**
   * Instance-level classification ceiling.
   * One of: classification-u, classification-s, classification-ts
   */
  @Column({ type: 'varchar', length: 50, name: 'classification_level', default: 'classification-u' })
  classificationLevel!: string;

  /** Master switch for federation. */
  @Column({ type: 'boolean', name: 'federation_enabled', default: false })
  federationEnabled: boolean = false;

  @CreateDateColumn({ name: 'created_at' })
  createdAt!: Date;

  @UpdateDateColumn({ name: 'updated_at' })
  updatedAt!: Date;
}
```

- [ ] **Step 4: Create FederationPeer entity**

Create `apps/api-gateway/src/modules/federation/entities/federation-peer.entity.ts`:

```typescript
import {
  Entity,
  Column,
  PrimaryColumn,
  CreateDateColumn,
  UpdateDateColumn,
} from 'typeorm';

/**
 * Represents a known peer instance. Rows are upserted when peers are
 * discovered (mDNS or seed list) and updated on each handshake.
 */
@Entity('federation_peers', { schema: 'sentinel' })
export class FederationPeer {
  /** The peer's stable instance UUID (from their handshake). */
  @PrimaryColumn({ type: 'uuid', name: 'instance_id' })
  instanceId!: string;

  @Column({ type: 'varchar', length: 255, name: 'display_name' })
  displayName!: string;

  /** WebSocket URL for this peer (e.g., ws://10.0.1.5:3100). */
  @Column({ type: 'varchar', length: 512 })
  url!: string;

  @Column({ type: 'varchar', length: 50, name: 'classification_level' })
  classificationLevel!: string;

  /** Current connection state: connected, disconnected, blocked. */
  @Column({ type: 'varchar', length: 20, default: 'disconnected' })
  status!: string;

  @Column({ type: 'timestamp', name: 'last_seen', nullable: true })
  lastSeen!: Date | null;

  /** Auto-assigned hex color for map rendering (e.g., #f97316). */
  @Column({ type: 'varchar', length: 7, nullable: true })
  color!: string | null;

  /** If true, this peer was manually added via seed list (not discovered via mDNS). */
  @Column({ type: 'boolean', name: 'is_seed', default: false })
  isSeed: boolean = false;

  @CreateDateColumn({ name: 'created_at' })
  createdAt!: Date;

  @UpdateDateColumn({ name: 'updated_at' })
  updatedAt!: Date;
}
```

- [ ] **Step 5: Create FederationPolicy entity**

Create `apps/api-gateway/src/modules/federation/entities/federation-policy.entity.ts`:

```typescript
import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  CreateDateColumn,
  UpdateDateColumn,
} from 'typeorm';

/**
 * Per-peer sharing policy. Controls what data flows to/from a specific peer.
 * If no policy exists for a peer, the default policy allows all entity types
 * within the classification ceiling (no geo bounds restriction).
 */
@Entity('federation_policies', { schema: 'sentinel' })
export class FederationPolicy {
  @PrimaryGeneratedColumn('uuid')
  id!: string;

  /** References FederationPeer.instanceId. */
  @Column({ type: 'uuid', name: 'peer_instance_id' })
  peerInstanceId!: string;

  /**
   * Entity types allowed for sharing. Empty array = all types allowed.
   * Values are EntityType enum strings (e.g., ['AIRCRAFT', 'SHIP']).
   */
  @Column({ type: 'jsonb', name: 'entity_types_allowed', default: () => "'[]'" })
  entityTypesAllowed: string[] = [];

  /**
   * Geographic bounding box for sharing. Null = no geo restriction.
   * Shape: { north, south, east, west } in decimal degrees.
   */
  @Column({ type: 'jsonb', name: 'geo_bounds', nullable: true })
  geoBounds!: { north: number; south: number; east: number; west: number } | null;

  /** Master switch for this peer's policy. */
  @Column({ type: 'boolean', default: true })
  enabled: boolean = true;

  @CreateDateColumn({ name: 'created_at' })
  createdAt!: Date;

  @UpdateDateColumn({ name: 'updated_at' })
  updatedAt!: Date;
}
```

- [ ] **Step 6: Create barrel export**

Create `apps/api-gateway/src/modules/federation/entities/index.ts`:

```typescript
export { FederationConfig } from './federation-config.entity';
export { FederationPeer } from './federation-peer.entity';
export { FederationPolicy } from './federation-policy.entity';
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `npx nx test api-gateway --testFile=src/modules/federation/entities/federation-entities.spec.ts`
Expected: PASS (3 tests)

- [ ] **Step 8: Commit**

```bash
git add apps/api-gateway/src/modules/federation/entities/
git commit -m "feat(federation): add TypeORM entities for config, peers, and policies"
```

---

### Task 3: Create Sharing Policy Service

**Files:**
- Create: `apps/api-gateway/src/modules/federation/sharing-policy.service.ts`
- Create: `apps/api-gateway/src/modules/federation/sharing-policy.service.spec.ts`

- [ ] **Step 1: Write the tests**

Create `apps/api-gateway/src/modules/federation/sharing-policy.service.spec.ts`:

```typescript
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
      // Entity uses enum values (UNCLASSIFIED, SECRET, TOP_SECRET)
      // Ceiling uses Keycloak role names (classification-u, classification-s, classification-ts)
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
      mockPolicyRepo.findOne.mockResolvedValue(null); // no policy = default allow
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test api-gateway --testFile=src/modules/federation/sharing-policy.service.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement SharingPolicyService**

Create `apps/api-gateway/src/modules/federation/sharing-policy.service.ts`:

```typescript
import { Injectable } from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPolicy } from './entities/federation-policy.entity';

/**
 * Mapping between Keycloak role names (used for instance-level classification
 * ceilings) and the Classification enum values (used on entities).
 *
 * Instance ceilings use Keycloak roles: classification-u, classification-s, classification-ts
 * Entity classification uses enum values: UNCLASSIFIED, SECRET, TOP_SECRET
 */
const ROLE_TO_CLASSIFICATION: Record<string, string> = {
  'classification-u': 'UNCLASSIFIED',
  'classification-s': 'SECRET',
  'classification-ts': 'TOP_SECRET',
};

/** Classification levels ordered from lowest to highest (entity enum values). */
const CLASSIFICATION_ORDER = ['UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET'] as const;

/** Instance-level classification roles ordered lowest to highest. */
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

  /**
   * Returns the lower of two instance classification levels (Keycloak role names).
   * This becomes the ceiling for a peer connection.
   */
  getClassificationCeiling(levelA: string, levelB: string): string {
    const indexA = INSTANCE_CLASSIFICATION_ORDER.indexOf(levelA as typeof INSTANCE_CLASSIFICATION_ORDER[number]);
    const indexB = INSTANCE_CLASSIFICATION_ORDER.indexOf(levelB as typeof INSTANCE_CLASSIFICATION_ORDER[number]);
    return INSTANCE_CLASSIFICATION_ORDER[Math.min(indexA, indexB)] ?? 'classification-u';
  }

  /**
   * Returns true if the entity's classification is at or below the connection ceiling.
   *
   * Entity classifications use enum values (UNCLASSIFIED, SECRET, TOP_SECRET).
   * Connection ceilings use Keycloak role names (classification-u, classification-s, classification-ts).
   * This method maps between the two vocabularies.
   */
  isClassificationAllowed(entityClassification: string, ceiling: string): boolean {
    const ceilingAsEnum = ROLE_TO_CLASSIFICATION[ceiling];
    if (!ceilingAsEnum) return false;
    const entityIndex = CLASSIFICATION_ORDER.indexOf(entityClassification as typeof CLASSIFICATION_ORDER[number]);
    const ceilingIndex = CLASSIFICATION_ORDER.indexOf(ceilingAsEnum as typeof CLASSIFICATION_ORDER[number]);
    if (entityIndex === -1 || ceilingIndex === -1) return false;
    return entityIndex <= ceilingIndex;
  }

  /**
   * Returns true if the entity type is in the allowlist.
   * Empty allowlist = all types allowed.
   */
  isEntityTypeAllowed(entityType: string, allowedTypes: string[]): boolean {
    if (allowedTypes.length === 0) return true;
    return allowedTypes.includes(entityType);
  }

  /**
   * Returns true if the position is within the geo bounds.
   * Null bounds = no restriction. Handles antimeridian crossing.
   */
  isInGeoBounds(lat: number, lon: number, bounds: GeoBounds | null): boolean {
    if (!bounds) return true;
    if (lat < bounds.south || lat > bounds.north) return false;

    // Antimeridian crossing: west > east means the bounds wrap around
    if (bounds.west > bounds.east) {
      return lon >= bounds.west || lon <= bounds.east;
    }
    return lon >= bounds.west && lon <= bounds.east;
  }

  /**
   * Full filtering pipeline: checks all policy rules for an entity
   * being sent to a specific peer.
   */
  async shouldShareEntity(
    entity: EntityForPolicy,
    peerInstanceId: string,
    connectionCeiling: string,
  ): Promise<boolean> {
    // Never re-share federated entities
    if (entity.sourceInstanceId) return false;

    // Classification check
    if (!this.isClassificationAllowed(entity.classification, connectionCeiling)) return false;

    // Fetch peer-specific policy (null = default allow)
    const policy = await this.policyRepo.findOne({
      where: { peerInstanceId },
    });

    // If policy exists but is disabled, block
    if (policy && !policy.enabled) return false;

    // Entity type check
    if (policy && !this.isEntityTypeAllowed(entity.entityType, policy.entityTypesAllowed)) return false;

    // Geo bounds check
    if (policy && !this.isInGeoBounds(entity.latitude, entity.longitude, policy.geoBounds)) return false;

    return true;
  }

  /**
   * Fetches the local instance's federation config (singleton row).
   */
  async getConfig(): Promise<FederationConfig | null> {
    return this.configRepo.findOne({ where: {}, order: { createdAt: 'ASC' } });
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test api-gateway --testFile=src/modules/federation/sharing-policy.service.spec.ts`
Expected: PASS (12 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/federation/sharing-policy.service.ts \
        apps/api-gateway/src/modules/federation/sharing-policy.service.spec.ts
git commit -m "feat(federation): add SharingPolicyService with classification, type, and geo filtering"
```

---

### Task 4: Create Federation Wire Protocol Types

**Files:**
- Create: `apps/api-gateway/src/modules/federation/federation.types.ts`

- [ ] **Step 1: Create the shared types file**

Create `apps/api-gateway/src/modules/federation/federation.types.ts`:

```typescript
/**
 * Federation wire protocol types.
 *
 * All message types use the `fed:` prefix to distinguish from
 * gateway-to-browser Socket.IO events on the /entities namespace.
 */

export const FEDERATION_PROTOCOL_VERSION = 1;
export const FEDERATION_PORT_DEFAULT = 3100;

/** Message types on the federation wire. */
export const FederationMessageType = {
  HANDSHAKE: 'fed:handshake',
  HEARTBEAT: 'fed:heartbeat',
  ENTITY_BATCH: 'fed:entity:batch',
  PRESENCE_UPDATE: 'fed:presence:update',
  PRESENCE_REMOVE: 'fed:presence:remove',
} as const;

export type FederationMessageTypeValue = typeof FederationMessageType[keyof typeof FederationMessageType];

/** Envelope wrapping all federation messages. */
export interface FederationMessage {
  type: FederationMessageTypeValue;
  sourceInstanceId: string;
  classificationLevel: string;
  payload: unknown;
}

/** Handshake payload — exchanged on connection. */
export interface HandshakePayload {
  instanceId: string;
  displayName: string;
  classificationLevel: string;
  protocolVersion: number;
}

/** A single entity in a federation entity batch. */
export interface FederatedEntity {
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
  sourceInstanceId: string;
  sourceInstanceName: string;
}

/** Payload for fed:entity:batch messages. */
export interface EntityBatchPayload {
  entities: FederatedEntity[];
}

/** Payload for fed:presence:update messages. */
export interface PresenceUpdatePayload {
  users: PresenceEntry[];
}

/** A single user's presence (map camera position). */
export interface PresenceEntry {
  userId: string;
  displayName: string;
  cameraCenter: { lat: number; lon: number };
  zoom: number;
  timestamp: number;
}

/** Payload for fed:presence:remove messages. */
export interface PresenceRemovePayload {
  userIds: string[];
}

/** Connection close reasons. */
export const FederationCloseReason = {
  VERSION_MISMATCH: 'version-mismatch',
  POLICY_VIOLATION: 'policy-violation',
  AUTH_FAILURE: 'auth-failure',
  SHUTDOWN: 'shutdown',
} as const;

/** Peer connection state as tracked by PeerManager. */
export type PeerConnectionState = 'connecting' | 'handshaking' | 'connected' | 'stale' | 'disconnected';
```

- [ ] **Step 2: Verify the types compile**

Run: `npx nx build api-gateway`
Expected: BUILD SUCCESSFUL (or at least no errors in federation.types.ts)

- [ ] **Step 3: Commit**

```bash
git add apps/api-gateway/src/modules/federation/federation.types.ts
git commit -m "feat(federation): add wire protocol types and constants"
```

---

## Chunk 2: Peer Manager, Federation Gateway, and Discovery

### Task 5: Create Peer Manager Service

The Peer Manager owns all outbound WebSocket connections to peers. It connects, performs the handshake, maintains heartbeats, handles reconnection, and routes inbound messages to the appropriate handlers.

**Files:**
- Create: `apps/api-gateway/src/modules/federation/peer-manager.service.ts`
- Create: `apps/api-gateway/src/modules/federation/peer-manager.service.spec.ts`

**Dependencies:** `ws` (WebSocket library — already available in Node.js, but we use the `ws` package for the server-side API). Install if not present: `npm install ws && npm install -D @types/ws`

- [ ] **Step 1: Write the tests**

Create `apps/api-gateway/src/modules/federation/peer-manager.service.spec.ts`:

```typescript
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test api-gateway --testFile=src/modules/federation/peer-manager.service.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Install ws package if needed**

Run: `npm ls ws 2>/dev/null || npm install ws @types/ws`

- [ ] **Step 4: Implement PeerManagerService**

Create `apps/api-gateway/src/modules/federation/peer-manager.service.ts`:

```typescript
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
  state: PeerConnectionState;
  lastHeartbeat: number;
  reconnectAttempts: number;
  reconnectTimer?: ReturnType<typeof setTimeout>;
}

/** Palette of colors auto-assigned to peers for map rendering. */
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
  private static readonly HEARTBEAT_TIMEOUT_MS = 30_000; // 3 missed heartbeats
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

    // Start heartbeat loop — sends heartbeats and checks for stale peers
    this.heartbeatInterval = setInterval(() => {
      const now = Date.now();
      for (const [instanceId, conn] of this.connections) {
        if (conn.state !== 'connected') continue;

        // Send heartbeat
        this.sendMessage(conn.ws, {
          type: FederationMessageType.HEARTBEAT,
          sourceInstanceId: this.localConfig.instanceId,
          classificationLevel: this.localConfig.classificationLevel,
          payload: {},
        });

        // Check for stale peers (3 missed heartbeats)
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

  /**
   * Fetches or creates the singleton federation config for this instance.
   * On first boot, generates a stable UUID and seeds from env vars.
   */
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

  /**
   * Validates an incoming handshake payload.
   */
  validateHandshake(payload: HandshakePayload): { valid: boolean; reason?: string } {
    if (!payload.instanceId) {
      return { valid: false, reason: 'missing-instance-id' };
    }
    if (payload.protocolVersion !== FEDERATION_PROTOCOL_VERSION) {
      return { valid: false, reason: FederationCloseReason.VERSION_MISMATCH };
    }
    return { valid: true };
  }

  /**
   * Initiates a WebSocket connection to a peer.
   */
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

  /**
   * Handles an incoming message from a peer WebSocket.
   */
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
        // Emit events for other services to consume
        this.eventEmitter.emit(`federation.${message.type}`, message);
        break;
      default:
        this.logger.warn(`Unknown message type from ${url}: ${(message as FederationMessage).type}`);
    }
  }

  /**
   * Handles a handshake response from a peer.
   */
  private async handleHandshakeResponse(ws: WebSocket, url: string, payload: HandshakePayload): Promise<void> {
    const validation = this.validateHandshake(payload);
    if (!validation.valid) {
      this.logger.warn(`Handshake rejected from ${url}: ${validation.reason}`);
      ws.close(4000, validation.reason);
      return;
    }

    // Check if already connected to this peer
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
      state: 'connected',
      lastHeartbeat: Date.now(),
      reconnectAttempts: 0,
    };

    this.connections.set(payload.instanceId, conn);

    // Upsert peer record in DB
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

        // Schedule reconnect unless explicitly blocked — must happen before
        // deleting from the map so the timer ref is trackable
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
      // Clean up the old connection entry before reconnecting
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

  /**
   * Sends a FederationMessage over a WebSocket.
   */
  sendMessage(ws: WebSocket, message: FederationMessage): void {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(message));
    }
  }

  /**
   * Sends a message to all connected peers.
   */
  broadcastToPeers(message: FederationMessage): void {
    for (const [, conn] of this.connections) {
      if (conn.state === 'connected') {
        this.sendMessage(conn.ws, message);
      }
    }
  }

  /**
   * Registers an inbound peer connection (called by FederationGateway
   * after a successful handshake exchange).
   */
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
      state: 'connected',
      lastHeartbeat: Date.now(),
      reconnectAttempts: 0,
    };

    this.connections.set(payload.instanceId, conn);

    // Set up message routing for this inbound connection
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

  /**
   * Returns the connection state for a given peer.
   */
  getPeerState(instanceId: string): PeerConnectionState {
    return this.connections.get(instanceId)?.state ?? 'disconnected';
  }

  /**
   * Returns info about all currently connected peers.
   */
  getConnectedPeers(): Array<{ instanceId: string; displayName: string; ceiling: string }> {
    const result: Array<{ instanceId: string; displayName: string; ceiling: string }> = [];
    for (const [, conn] of this.connections) {
      if (conn.state === 'connected') {
        result.push({
          instanceId: conn.instanceId,
          displayName: conn.displayName,
          ceiling: conn.connectionCeiling,
        });
      }
    }
    return result;
  }

  /**
   * Returns the connection ceiling for a specific peer.
   */
  getConnectionCeiling(instanceId: string): string | null {
    return this.connections.get(instanceId)?.connectionCeiling ?? null;
  }

  /**
   * Auto-assigns a color from the palette based on peer index.
   */
  private assignPeerColor(instanceId: string): string {
    // Deterministic color based on instance ID hash
    let hash = 0;
    for (let i = 0; i < instanceId.length; i++) {
      hash = ((hash << 5) - hash + instanceId.charCodeAt(i)) | 0;
    }
    return PEER_COLORS[Math.abs(hash) % PEER_COLORS.length];
  }
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `npx nx test api-gateway --testFile=src/modules/federation/peer-manager.service.spec.ts`
Expected: PASS (5 tests)

- [ ] **Step 6: Commit**

```bash
git add apps/api-gateway/src/modules/federation/peer-manager.service.ts \
        apps/api-gateway/src/modules/federation/peer-manager.service.spec.ts
git commit -m "feat(federation): add PeerManagerService with connections, handshake, and heartbeats"
```

---

### Task 6: Create Federation WebSocket Gateway

The Federation Gateway listens on a dedicated port (default 3100) for incoming peer connections. It performs auth (PSK in dev), delegates handshake to PeerManager, and routes incoming messages.

**Files:**
- Create: `apps/api-gateway/src/modules/federation/federation.gateway.ts`
- Create: `apps/api-gateway/src/modules/federation/federation.gateway.spec.ts`

- [ ] **Step 1: Write the tests**

Create `apps/api-gateway/src/modules/federation/federation.gateway.spec.ts`:

```typescript
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
      // Re-read the PSK
      const gw = new FederationGateway(
        { get: () => undefined } as unknown as ConfigService,
        mockPeerManager as unknown as PeerManagerService,
        mockSharingPolicy as unknown as SharingPolicyService,
      );
      expect(gw.verifyPsk('anything')).toBe(false);
    });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test api-gateway --testFile=src/modules/federation/federation.gateway.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement FederationGateway**

Create `apps/api-gateway/src/modules/federation/federation.gateway.ts`:

```typescript
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

/**
 * WebSocket server listening on the federation port (default 3100)
 * for incoming peer connections. Handles auth (PSK in dev, mTLS in prod)
 * and delegates to PeerManager for handshake and message routing.
 */
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

  /**
   * Verifies a pre-shared key for dev authentication.
   */
  verifyPsk(token: string): boolean {
    if (!this.psk) return false;
    return token === this.psk;
  }

  /**
   * Handles a new incoming WebSocket connection from a peer.
   */
  private handleConnection(ws: WebSocket, req: IncomingMessage): void {
    const remoteAddr = req.socket.remoteAddress ?? 'unknown';
    this.logger.log(`Incoming federation connection from ${remoteAddr}`);

    // PSK auth: expect token in query string ?psk=xxx
    const url = new URL(req.url ?? '', `http://${req.headers.host}`);
    const pskToken = url.searchParams.get('psk');

    if (this.psk && !this.verifyPsk(pskToken ?? '')) {
      this.logger.warn(`Auth failed for ${remoteAddr}: invalid PSK`);
      ws.close(4003, FederationCloseReason.AUTH_FAILURE);
      return;
    }

    // Set up a timeout for handshake — peer must send handshake within 10s
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

  /**
   * Processes an incoming handshake from a peer and responds.
   */
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

    // Send our handshake back
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

    // Register the peer connection and set up message routing
    const peerUrl = `ws://${remoteAddr}:${this.port}`;
    await this.peerManager.registerIncomingPeer(ws, payload, peerUrl);
    this.logger.log(`Handshake accepted from ${payload.displayName} (${payload.instanceId}) at ${remoteAddr}`);
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test api-gateway --testFile=src/modules/federation/federation.gateway.spec.ts`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/federation/federation.gateway.ts \
        apps/api-gateway/src/modules/federation/federation.gateway.spec.ts
git commit -m "feat(federation): add FederationGateway WebSocket server on dedicated port"
```

---

### Task 7: Create Discovery Service

The Discovery Service handles mDNS advertisement and listening for LAN peers, plus a seed list for WAN peers. Discovered peers are passed to PeerManager for connection.

**Files:**
- Create: `apps/api-gateway/src/modules/federation/discovery.service.ts`
- Create: `apps/api-gateway/src/modules/federation/discovery.service.spec.ts`

**Dependencies:** `npm install multicast-dns` (mDNS library), `npm install -D @types/multicast-dns`

- [ ] **Step 1: Write the tests**

Create `apps/api-gateway/src/modules/federation/discovery.service.spec.ts`:

```typescript
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test api-gateway --testFile=src/modules/federation/discovery.service.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement DiscoveryService**

Create `apps/api-gateway/src/modules/federation/discovery.service.ts`:

```typescript
import {
  Injectable,
  Logger,
  OnModuleInit,
  OnModuleDestroy,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { PeerManagerService } from './peer-manager.service';
import { FEDERATION_PORT_DEFAULT } from './federation.types';

/**
 * Discovers peer Sentinel instances via mDNS (LAN) and seed lists (WAN).
 * Passes discovered peers to PeerManager for connection.
 *
 * mDNS uses the `multicast-dns` package (optional — if not available,
 * falls back to seed-list-only mode). Service type: _sentinel-fed._tcp
 */
@Injectable()
export class DiscoveryService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(DiscoveryService.name);
  private readonly port: number;
  private readonly psk: string | undefined;
  private mdns: unknown | null = null;
  private seedPollTimer?: ReturnType<typeof setInterval>;

  private static readonly SERVICE_TYPE = '_sentinel-fed._tcp.local';
  private static readonly MDNS_INTERVAL_MS = 30_000;
  private static readonly SEED_POLL_INTERVAL_MS = 30_000;

  constructor(
    private readonly configService: ConfigService,
    private readonly peerManager: PeerManagerService,
  ) {
    this.port = this.configService.get<number>('FEDERATION_PORT', FEDERATION_PORT_DEFAULT);
    this.psk = this.configService.get<string>('FEDERATION_PSK');
  }

  async onModuleInit(): Promise<void> {
    const config = await this.peerManager.getOrCreateConfig();
    if (!config.federationEnabled) {
      this.logger.log('Federation disabled — discovery not started');
      return;
    }

    // Try to load multicast-dns (optional dependency)
    try {
      const mDNS = await import('multicast-dns');
      this.mdns = mDNS.default();
      this.startMdnsAdvertisement(config.instanceId, config.displayName);
      this.startMdnsListening();
      this.logger.log('mDNS discovery started');
    } catch {
      this.logger.warn('multicast-dns not available — using seed list only');
    }

    // Start seed list polling
    const seeds = this.configService.get<string>('FEDERATION_SEEDS', '');
    if (seeds) {
      this.startSeedPolling(seeds);
    }
  }

  async onModuleDestroy(): Promise<void> {
    if (this.seedPollTimer) clearInterval(this.seedPollTimer);
    if (this.mdns && typeof (this.mdns as { destroy: () => void }).destroy === 'function') {
      (this.mdns as { destroy: () => void }).destroy();
    }
  }

  /**
   * Parses a comma-separated seed list into URLs.
   */
  parseSeedList(seeds: string): string[] {
    if (!seeds.trim()) return [];
    return seeds.split(',').map(s => s.trim()).filter(Boolean);
  }

  /**
   * Builds a WebSocket URL for connecting to a peer.
   */
  buildPeerUrl(host: string, port: number): string {
    const base = `ws://${host}:${port}`;
    return this.psk ? `${base}?psk=${this.psk}` : base;
  }

  /**
   * Advertises this instance via mDNS responses.
   * Uses the multicast-dns library's query/response pattern.
   */
  private startMdnsAdvertisement(instanceId: string, displayName: string): void {
    const mdns = this.mdns as { on: (event: string, cb: (...args: unknown[]) => void) => void; respond: (response: unknown) => void };

    mdns.on('query', (query: { questions: Array<{ name: string }> }) => {
      const isForUs = query.questions.some(
        (q: { name: string }) => q.name === DiscoveryService.SERVICE_TYPE,
      );
      if (!isForUs) return;

      mdns.respond({
        answers: [{
          name: DiscoveryService.SERVICE_TYPE,
          type: 'SRV',
          data: { port: this.port, target: `${instanceId}.local` },
        }, {
          name: DiscoveryService.SERVICE_TYPE,
          type: 'TXT',
          data: [`id=${instanceId}`, `name=${displayName}`],
        }],
      });
    });

    this.logger.log(`mDNS: advertising ${displayName} (${instanceId}) on port ${this.port}`);
  }

  /**
   * Listens for peer mDNS announcements and triggers connections.
   */
  private startMdnsListening(): void {
    const mdns = this.mdns as { on: (event: string, cb: (...args: unknown[]) => void) => void; query: (q: unknown) => void };

    mdns.on('response', (response: { answers: Array<{ name: string; type: string; data: unknown }> }) => {
      const srvAnswer = response.answers.find(
        (a: { name: string; type: string }) => a.name === DiscoveryService.SERVICE_TYPE && a.type === 'SRV',
      );
      const txtAnswer = response.answers.find(
        (a: { name: string; type: string }) => a.name === DiscoveryService.SERVICE_TYPE && a.type === 'TXT',
      );

      if (!srvAnswer || !txtAnswer) return;

      const srv = srvAnswer.data as { port: number; target: string };
      const txt = (txtAnswer.data as string[]).reduce((acc: Record<string, string>, entry: string) => {
        const [key, val] = entry.split('=');
        acc[key] = val;
        return acc;
      }, {});

      const peerId = txt['id'];
      if (!peerId || this.peerManager.getPeerState(peerId) !== 'disconnected') return;

      const host = srv.target.replace('.local', '');
      const url = this.buildPeerUrl(host, srv.port);
      this.logger.log(`mDNS: discovered peer ${txt['name']} (${peerId}) at ${url}`);
      this.peerManager.connectToPeer(url).catch(err => {
        this.logger.debug(`mDNS connect to ${url} failed: ${err.message}`);
      });
    });

    // Send initial query
    mdns.query({ questions: [{ name: DiscoveryService.SERVICE_TYPE, type: 'SRV' }] });

    // Periodic re-query
    setInterval(() => {
      mdns.query({ questions: [{ name: DiscoveryService.SERVICE_TYPE, type: 'SRV' }] });
    }, DiscoveryService.MDNS_INTERVAL_MS);

    this.logger.log('mDNS: listening for peer announcements');
  }

  /** Tracks which seed URLs are currently being connected or connected. */
  private readonly activeSeedUrls = new Set<string>();

  private startSeedPolling(seeds: string): void {
    const urls = this.parseSeedList(seeds);
    this.logger.log(`Seed list: polling ${urls.length} peers every ${DiscoveryService.SEED_POLL_INTERVAL_MS}ms`);

    const pollSeeds = async () => {
      for (const url of urls) {
        // Skip URLs that already have an active connection attempt
        if (this.activeSeedUrls.has(url)) continue;

        // Check if any connected peer has this URL
        const connectedPeers = this.peerManager.getConnectedPeers();
        const alreadyConnected = connectedPeers.some(p => p.instanceId); // Will always be true if connected
        // Since we can't map URL→instanceId before handshake, track via activeSeedUrls
        this.activeSeedUrls.add(url);
        this.peerManager.connectToPeer(url)
          .catch(err => {
            this.logger.debug(`Seed connect to ${url} failed: ${err.message}`);
            this.activeSeedUrls.delete(url);
          });
      }
    };

    // Poll immediately, then on interval
    pollSeeds();
    this.seedPollTimer = setInterval(pollSeeds, DiscoveryService.SEED_POLL_INTERVAL_MS);
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test api-gateway --testFile=src/modules/federation/discovery.service.spec.ts`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/federation/discovery.service.ts \
        apps/api-gateway/src/modules/federation/discovery.service.spec.ts
git commit -m "feat(federation): add DiscoveryService with mDNS and seed list support"
```

---

## Chunk 3: REST Controller, Module Wiring, and Kafka Integration

### Task 8: Create Federation REST Controller

Admin endpoints for managing federation configuration, peers, and policies.

**Files:**
- Create: `apps/api-gateway/src/modules/federation/federation.controller.ts`
- Create: `apps/api-gateway/src/modules/federation/federation.controller.spec.ts`

- [ ] **Step 1: Write the tests**

Create `apps/api-gateway/src/modules/federation/federation.controller.spec.ts`:

```typescript
import { Test, TestingModule } from '@nestjs/testing';
import { HttpException, HttpStatus } from '@nestjs/common';
import { getRepositoryToken } from '@nestjs/typeorm';
import { FederationController } from './federation.controller';
import { PeerManagerService } from './peer-manager.service';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPeer } from './entities/federation-peer.entity';
import { FederationPolicy } from './entities/federation-policy.entity';

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
    }).compile();

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test api-gateway --testFile=src/modules/federation/federation.controller.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement FederationController**

Create `apps/api-gateway/src/modules/federation/federation.controller.ts`:

```typescript
import {
  Controller,
  Get,
  Put,
  Post,
  Delete,
  Body,
  Param,
  UseGuards,
} from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { Roles } from '../auth/decorators/roles.decorator';
import { PeerManagerService } from './peer-manager.service';
import { FederationConfig } from './entities/federation-config.entity';
import { FederationPeer } from './entities/federation-peer.entity';
import { FederationPolicy } from './entities/federation-policy.entity';

interface UpdateConfigDto {
  displayName?: string;
  federationEnabled?: boolean;
}

interface AddSeedPeerDto {
  url: string;
  displayName: string;
}

interface UpdatePolicyDto {
  entityTypesAllowed?: string[];
  geoBounds?: { north: number; south: number; east: number; west: number } | null;
  enabled?: boolean;
}

@Controller('api/v1/federation')
@UseGuards(JwtAuthGuard)
@Roles('sentinel-admin')
export class FederationController {
  constructor(
    private readonly peerManager: PeerManagerService,
    @InjectRepository(FederationConfig)
    private readonly configRepo: Repository<FederationConfig>,
    @InjectRepository(FederationPeer)
    private readonly peerRepo: Repository<FederationPeer>,
    @InjectRepository(FederationPolicy)
    private readonly policyRepo: Repository<FederationPolicy>,
  ) {}

  @Get('config')
  async getConfig(): Promise<FederationConfig> {
    return this.peerManager.getOrCreateConfig();
  }

  @Put('config')
  async updateConfig(@Body() body: UpdateConfigDto): Promise<FederationConfig> {
    const config = await this.peerManager.getOrCreateConfig();
    if (body.displayName !== undefined) config.displayName = body.displayName;
    if (body.federationEnabled !== undefined) config.federationEnabled = body.federationEnabled;
    return this.configRepo.save(config);
  }

  @Get('peers')
  async getPeers(): Promise<FederationPeer[]> {
    return this.peerRepo.find();
  }

  @Get('status')
  async getStatus(): Promise<{ connectedPeers: Array<{ instanceId: string; displayName: string; ceiling: string }> }> {
    return { connectedPeers: this.peerManager.getConnectedPeers() };
  }

  @Post('peers')
  async addSeedPeer(@Body() body: AddSeedPeerDto): Promise<FederationPeer> {
    const peer = new FederationPeer();
    peer.instanceId = crypto.randomUUID();
    peer.url = body.url;
    peer.displayName = body.displayName;
    peer.classificationLevel = 'classification-u';
    peer.status = 'disconnected';
    peer.isSeed = true;
    return this.peerRepo.save(peer);
  }

  @Delete('peers/:instanceId')
  async removePeer(@Param('instanceId') instanceId: string): Promise<{ message: string }> {
    await this.policyRepo.delete({ peerInstanceId: instanceId });
    await this.peerRepo.delete({ instanceId });
    return { message: `Peer ${instanceId} removed` };
  }

  @Put('policies/:peerInstanceId')
  async updatePolicy(
    @Param('peerInstanceId') peerInstanceId: string,
    @Body() body: UpdatePolicyDto,
  ): Promise<FederationPolicy> {
    let policy = await this.policyRepo.findOne({ where: { peerInstanceId } });

    if (!policy) {
      policy = this.policyRepo.create({ peerInstanceId });
    }

    if (body.entityTypesAllowed !== undefined) policy.entityTypesAllowed = body.entityTypesAllowed;
    if (body.geoBounds !== undefined) policy.geoBounds = body.geoBounds;
    if (body.enabled !== undefined) policy.enabled = body.enabled;

    return this.policyRepo.save(policy);
  }

  @Get('policies/:peerInstanceId')
  async getPolicy(@Param('peerInstanceId') peerInstanceId: string): Promise<FederationPolicy | null> {
    return this.policyRepo.findOne({ where: { peerInstanceId } });
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test api-gateway --testFile=src/modules/federation/federation.controller.spec.ts`
Expected: PASS (7 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/federation/federation.controller.ts \
        apps/api-gateway/src/modules/federation/federation.controller.spec.ts
git commit -m "feat(federation): add FederationController with admin REST endpoints"
```

---

### Task 9: Wire Up Federation Module

Create the NestJS module that ties everything together, and register it in the AppModule.

**Files:**
- Create: `apps/api-gateway/src/modules/federation/federation.module.ts`
- Modify: `apps/api-gateway/src/app.module.ts`

- [ ] **Step 1: Create FederationModule**

Create `apps/api-gateway/src/modules/federation/federation.module.ts`:

```typescript
import { Module } from '@nestjs/common';
import { ConfigModule } from '@nestjs/config';
import { TypeOrmModule } from '@nestjs/typeorm';
import { FederationConfig, FederationPeer, FederationPolicy } from './entities';
import { SharingPolicyService } from './sharing-policy.service';
import { PeerManagerService } from './peer-manager.service';
import { FederationGateway } from './federation.gateway';
import { DiscoveryService } from './discovery.service';
import { FederationController } from './federation.controller';

@Module({
  imports: [
    ConfigModule,
    TypeOrmModule.forFeature([FederationConfig, FederationPeer, FederationPolicy]),
  ],
  controllers: [FederationController],
  providers: [
    SharingPolicyService,
    PeerManagerService,
    FederationGateway,
    DiscoveryService,
  ],
  exports: [PeerManagerService, SharingPolicyService],
})
export class FederationModule {}
```

- [ ] **Step 2: Register FederationModule in AppModule**

In `apps/api-gateway/src/app.module.ts`, add imports:

```typescript
import { EventEmitterModule } from '@nestjs/event-emitter';
import { FederationModule } from './modules/federation/federation.module';
```

And add both to the `imports` array (EventEmitterModule.forRoot() should be called once at the app level):

```typescript
    // Event emitter for federation cross-module communication
    EventEmitterModule.forRoot(),

    // Feature modules
    AuthModule,
    ...
    LocationsModule,
    FederationModule,
```

- [ ] **Step 3: Install @nestjs/event-emitter if needed**

Run: `npm ls @nestjs/event-emitter 2>/dev/null || npm install @nestjs/event-emitter`

- [ ] **Step 4: Verify the application builds**

Run: `npx nx build api-gateway`
Expected: BUILD SUCCESSFUL

- [ ] **Step 5: Run all federation tests**

Run: `npx nx test api-gateway --testPathPattern=federation`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add apps/api-gateway/src/modules/federation/federation.module.ts \
        apps/api-gateway/src/app.module.ts
git commit -m "feat(federation): wire up FederationModule and register in AppModule"
```

---

### Task 10: Modify Kafka Consumer for Federation Outbound

Add federation entity forwarding to the existing Kafka Consumer so local entity events are published to the `federation.entity.outbound` topic for the Federation Module to consume.

**Files:**
- Modify: `apps/api-gateway/src/modules/gateway/kafka-consumer.service.ts`

- [ ] **Step 1: Add federation producer to KafkaConsumerService**

In `apps/api-gateway/src/modules/gateway/kafka-consumer.service.ts`:

1. Add import at the top:
```typescript
import { Producer } from 'kafkajs';
```

2. Add a producer field and import the topic constant:
```typescript
import { KafkaTopics } from '@sentinel/common';
```
And add a producer field:
```typescript
  private producer!: Producer;
```

3. In `onModuleInit()`, after `this.consumer.connect()`, create and connect a producer:
```typescript
      this.producer = this.kafka.producer();
      await this.producer.connect();
      this.logger.log('Kafka federation producer connected');
```

4. In `onModuleDestroy()`, disconnect the producer:
```typescript
      await this.producer?.disconnect();
```

5. Add a method to forward entity events:
```typescript
  /**
   * Forwards a local entity event to the federation outbound topic.
   * Only local entities (no sourceInstanceId) are forwarded.
   */
  private async forwardToFederation(raw: RawEntityPositionEvent, eventType: string): Promise<void> {
    if (!this.kafkaConnected) return;
    try {
      await this.producer.send({
        topic: KafkaTopics.FEDERATION_ENTITY_OUTBOUND,
        messages: [{
          key: raw.entity_id,
          value: JSON.stringify({ ...raw, eventType }),
        }],
      });
    } catch (error) {
      this.logger.debug(`Failed to forward to federation topic: ${error}`);
    }
  }
```

6. Call `forwardToFederation` in `handlePositionEvent`, `handleEntityCreatedEvent`, and `handleEntityUpdatedEvent` after the existing `bufferUpdate` call:
```typescript
    this.bufferUpdate(raw, 'updated');
    this.forwardToFederation(raw, 'updated');
```

- [ ] **Step 2: Verify existing tests still pass**

Run: `npx nx test api-gateway`
Expected: ALL PASS (existing + federation tests)

- [ ] **Step 3: Commit**

```bash
git add apps/api-gateway/src/modules/gateway/kafka-consumer.service.ts
git commit -m "feat(federation): forward local entity events to federation outbound topic"
```
