# Entity & Track Ageout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement tiered entity ageout (LIVE → STALE → AGED_OUT) with per-feed configurable thresholds, a 15-second backend polling job, and client-side staleness rendering.

**Architecture:** New `AgeoutService` polling job in entity-service transitions entities through ageout states based on `lastSeenAt` vs configurable thresholds stored in a `feed_ageout_config` table. Restoration happens automatically in the existing ingest pipeline. Frontend applies client-side staleness visual treatment using `lastSeenAt` + threshold configs for immediate feedback. Three new Kafka topics (`events.entity.stale`, `events.entity.agedout`, `events.entity.restored`) flow through the WebSocket gateway to connected clients.

**Tech Stack:** NestJS (entity-service), TypeORM + PostgreSQL/PostGIS, @nestjs/schedule, Kafka, Angular 19 + CesiumJS, Jest

**Spec:** `docs/superpowers/specs/2026-03-21-entity-track-ageout-design.md`

---

## File Structure

### New Files
| File | Responsibility |
|------|---------------|
| `apps/entity-service/src/entities/ageout-config.entity.ts` | TypeORM entity for `feed_ageout_config` table |
| `apps/entity-service/src/entities/dto/ageout-config.dto.ts` | DTOs for ageout config CRUD API |
| `apps/entity-service/src/entities/ageout-config.controller.ts` | REST endpoints for managing ageout thresholds |
| `apps/entity-service/src/entities/ageout.service.ts` | Polling job, state transitions, Kafka event emission |
| `apps/entity-service/src/entities/ageout.service.spec.ts` | Tests for ageout polling and state transitions |
| `apps/entity-service/src/entities/ageout-config.controller.spec.ts` | Tests for config CRUD controller |

### Modified Files
| File | Change |
|------|--------|
| `apps/entity-service/src/entities/enums.ts` | Add `AgeoutState` enum |
| `apps/entity-service/src/entities/entity.entity.ts` | Add `ageoutState` column |
| `libs/common/src/kafka-topics.ts` | Add 3 new topic constants |
| `apps/entity-service/src/entities/entity.repository.ts` | Add `ageoutState` to `ExistingEntityInfo`, ageout filter in queries, `ageoutState = 'LIVE'` in bulk update |
| `apps/entity-service/src/entities/ingest.consumer.ts` | Emit `events.entity.restored` for entities transitioning back to LIVE |
| `apps/entity-service/src/entities/entity.module.ts` | Register new entity, service, controller |
| `apps/entity-service/src/app.module.ts` | Import `ScheduleModule.forRoot()` |
| `apps/api-gateway/src/modules/gateway/entity.gateway.ts` | Add `broadcastAgeoutEvent()` for non-viewport-filtered broadcasts |
| `apps/web/src/app/shared/models/entity.model.ts` | Add `ageoutState` field to `Entity` interface |
| `apps/web/src/app/core/services/websocket.service.ts` | Listen for ageout WebSocket events |
| `apps/web/src/app/core/services/entity.service.ts` | Handle ageout events, replace hardcoded stale threshold with config-aware logic |
| `apps/web/src/app/features/map/map.component.ts` | Staleness visual treatment (opacity, dimming) |

---

### Task 1: Add AgeoutState enum and Kafka topics

**Files:**
- Modify: `apps/entity-service/src/entities/enums.ts`
- Modify: `libs/common/src/kafka-topics.ts`

- [ ] **Step 1: Add AgeoutState enum**

In `apps/entity-service/src/entities/enums.ts`, add after the `CharacterizationState` enum:

```typescript
export enum AgeoutState {
  LIVE = 'LIVE',
  STALE = 'STALE',
  AGED_OUT = 'AGED_OUT',
}
```

- [ ] **Step 2: Add Kafka topic constants**

In `libs/common/src/kafka-topics.ts`, add to the `KafkaTopics` object after the existing entity events:

```typescript
  // Entity ageout events
  ENTITY_STALE: 'events.entity.stale',
  ENTITY_AGED_OUT: 'events.entity.agedout',
  ENTITY_RESTORED: 'events.entity.restored',
```

- [ ] **Step 3: Commit**

```bash
git add apps/entity-service/src/entities/enums.ts libs/common/src/kafka-topics.ts
git commit -m "feat(entity-service): add AgeoutState enum and ageout Kafka topics"
```

---

### Task 2: Add ageoutState column to EntityRecord

**Files:**
- Modify: `apps/entity-service/src/entities/entity.entity.ts`

- [ ] **Step 1: Add ageoutState column**

In `apps/entity-service/src/entities/entity.entity.ts`:

1. Add `AgeoutState` to the imports from `./enums`
2. Add a new `@Index` decorator on the class: `@Index('idx_entities_ageout_state', ['ageoutState'])`
3. Add the column after the `deleted`/`deletedAt` fields:

```typescript
  @Column({ type: 'varchar', default: 'LIVE' })
  ageoutState!: string;
```

- [ ] **Step 2: Commit**

```bash
git add apps/entity-service/src/entities/entity.entity.ts
git commit -m "feat(entity-service): add ageoutState column to EntityRecord"
```

---

### Task 3: Create feed_ageout_config TypeORM entity

**Files:**
- Create: `apps/entity-service/src/entities/ageout-config.entity.ts`

- [ ] **Step 1: Write the entity**

```typescript
import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  CreateDateColumn,
  UpdateDateColumn,
  Index,
} from 'typeorm';

/**
 * Configurable ageout thresholds per feed source.
 * Threshold resolution: feed-specific (feedId + sourceType) → source-type default (feedId NULL) → global default (both NULL).
 */
@Entity('feed_ageout_config', { schema: 'sentinel' })
@Index('idx_ageout_config_feed_source', ['feedId', 'sourceType'], {
  unique: true,
  where: '"feedId" IS NOT NULL AND "sourceType" IS NOT NULL',
})
@Index('idx_ageout_config_source_default', ['sourceType'], {
  unique: true,
  where: '"feedId" IS NULL AND "sourceType" IS NOT NULL',
})
export class AgeoutConfigRecord {
  @PrimaryGeneratedColumn('uuid')
  id!: string;

  @Column({ type: 'uuid', nullable: true })
  feedId!: string | null;

  @Column({ type: 'varchar', nullable: true })
  sourceType!: string | null;

  @Column({ type: 'int' })
  staleThresholdMs!: number;

  @Column({ type: 'int' })
  ageoutThresholdMs!: number;

  @CreateDateColumn()
  createdAt!: Date;

  @UpdateDateColumn()
  updatedAt!: Date;
}
```

- [ ] **Step 2: Create the global default unique index manually**

TypeORM's `@Index` decorator doesn't support expression indexes like `((1))`. Add this index in the `AgeoutService.onModuleInit()` (done in Task 6). For now, the entity is sufficient.

- [ ] **Step 3: Commit**

```bash
git add apps/entity-service/src/entities/ageout-config.entity.ts
git commit -m "feat(entity-service): add AgeoutConfigRecord TypeORM entity"
```

---

### Task 4: Create ageout config DTOs

**Files:**
- Create: `apps/entity-service/src/entities/dto/ageout-config.dto.ts`

- [ ] **Step 1: Write the DTOs**

```typescript
import { IsOptional, IsString, IsInt, IsUUID, Min, Validate, ValidatorConstraint, ValidatorConstraintInterface, ValidationArguments } from 'class-validator';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';

@ValidatorConstraint({ name: 'isGreaterThanStale', async: false })
class IsGreaterThanStaleConstraint implements ValidatorConstraintInterface {
  validate(value: number, args: ValidationArguments): boolean {
    const obj = args.object as AgeoutConfigDto;
    return value > obj.staleThresholdMs;
  }

  defaultMessage(): string {
    return 'ageoutThresholdMs must be greater than staleThresholdMs';
  }
}

export class AgeoutConfigDto {
  @ApiPropertyOptional({ description: 'Feed UUID. Omit for source-type or global default.' })
  @IsOptional()
  @IsUUID()
  feedId?: string;

  @ApiPropertyOptional({ description: 'EntitySource value (e.g. ADS_B, AIS). Omit for global default.' })
  @IsOptional()
  @IsString()
  sourceType?: string;

  @ApiProperty({ description: 'Milliseconds after lastSeenAt before marking STALE', example: 60000 })
  @IsInt()
  @Min(1000)
  staleThresholdMs!: number;

  @ApiProperty({ description: 'Milliseconds after lastSeenAt before marking AGED_OUT', example: 300000 })
  @IsInt()
  @Min(1000)
  @Validate(IsGreaterThanStaleConstraint)
  ageoutThresholdMs!: number;
}

export class AgeoutConfigResponseDto {
  id!: string;
  feedId!: string | null;
  sourceType!: string | null;
  staleThresholdMs!: number;
  ageoutThresholdMs!: number;
  createdAt!: string;
  updatedAt!: string;
}
```

- [ ] **Step 2: Commit**

```bash
git add apps/entity-service/src/entities/dto/ageout-config.dto.ts
git commit -m "feat(entity-service): add ageout config DTOs with validation"
```

---

### Task 5: Create ageout config controller with tests

**Files:**
- Create: `apps/entity-service/src/entities/ageout-config.controller.ts`
- Create: `apps/entity-service/src/entities/ageout-config.controller.spec.ts`

- [ ] **Step 1: Write the failing test**

Create `apps/entity-service/src/entities/ageout-config.controller.spec.ts`:

```typescript
import { Test, TestingModule } from '@nestjs/testing';
import { AgeoutConfigController } from './ageout-config.controller';
import { DataSource, Repository } from 'typeorm';
import { getRepositoryToken } from '@nestjs/typeorm';
import { AgeoutConfigRecord } from './ageout-config.entity';

describe('AgeoutConfigController', () => {
  let controller: AgeoutConfigController;
  let repo: {
    find: jest.Mock;
    findOne: jest.Mock;
    save: jest.Mock;
    delete: jest.Mock;
  };

  beforeEach(async () => {
    repo = {
      find: jest.fn(),
      findOne: jest.fn(),
      save: jest.fn(),
      delete: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      controllers: [AgeoutConfigController],
      providers: [
        { provide: getRepositoryToken(AgeoutConfigRecord), useValue: repo },
      ],
    }).compile();

    controller = module.get<AgeoutConfigController>(AgeoutConfigController);
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  describe('findAll', () => {
    it('should return all ageout configs', async () => {
      const configs = [
        { id: 'uuid-1', sourceType: 'ADS_B', staleThresholdMs: 60000, ageoutThresholdMs: 300000 },
      ];
      repo.find.mockResolvedValue(configs);

      const result = await controller.findAll();
      expect(result).toEqual(configs);
      expect(repo.find).toHaveBeenCalled();
    });
  });

  describe('findBySourceType', () => {
    it('should return config for source type', async () => {
      const config = { id: 'uuid-1', sourceType: 'ADS_B', feedId: null, staleThresholdMs: 60000, ageoutThresholdMs: 300000 };
      repo.findOne.mockResolvedValue(config);

      const result = await controller.findBySourceType('ADS_B', undefined);
      expect(result).toEqual(config);
      expect(repo.findOne).toHaveBeenCalledWith({
        where: { sourceType: 'ADS_B', feedId: null },
      });
    });
  });

  describe('upsert', () => {
    it('should create new config when none exists', async () => {
      repo.findOne.mockResolvedValue(null);
      const dto = { sourceType: 'AIS', staleThresholdMs: 600000, ageoutThresholdMs: 1800000 };
      const saved = { id: 'uuid-2', feedId: null, ...dto };
      repo.save.mockResolvedValue(saved);

      const result = await controller.upsert(dto as any);
      expect(result).toEqual(saved);
    });

    it('should update existing config', async () => {
      const existing = { id: 'uuid-1', sourceType: 'ADS_B', feedId: null, staleThresholdMs: 60000, ageoutThresholdMs: 300000 };
      repo.findOne.mockResolvedValue(existing);
      const dto = { sourceType: 'ADS_B', staleThresholdMs: 30000, ageoutThresholdMs: 120000 };
      const updated = { ...existing, ...dto };
      repo.save.mockResolvedValue(updated);

      const result = await controller.upsert(dto as any);
      expect(result.staleThresholdMs).toBe(30000);
    });
  });

  describe('remove', () => {
    it('should delete config by id', async () => {
      repo.delete.mockResolvedValue({ affected: 1 });

      await controller.remove('uuid-1');
      expect(repo.delete).toHaveBeenCalledWith('uuid-1');
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test entity-service -- --testPathPattern=ageout-config.controller.spec`
Expected: FAIL — `AgeoutConfigController` not found

- [ ] **Step 3: Write the controller**

Create `apps/entity-service/src/entities/ageout-config.controller.ts`:

```typescript
import { Controller, Get, Put, Delete, Param, Query, Body } from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { AgeoutConfigRecord } from './ageout-config.entity';
import { AgeoutConfigDto } from './dto/ageout-config.dto';

// Note: Auth guards (JwtAuthGuard with sentinel-operator/sentinel-admin roles)
// are enforced at the api-gateway proxy level, consistent with EntityController.
@Controller('entities/ageout-config')
export class AgeoutConfigController {
  constructor(
    @InjectRepository(AgeoutConfigRecord)
    private readonly configRepo: Repository<AgeoutConfigRecord>,
  ) {}

  @Get()
  async findAll(): Promise<AgeoutConfigRecord[]> {
    return this.configRepo.find();
  }

  @Get(':sourceType')
  async findBySourceType(
    @Param('sourceType') sourceType: string,
    @Query('feedId') feedId?: string,
  ): Promise<AgeoutConfigRecord | null> {
    return this.configRepo.findOne({
      where: { sourceType, feedId: feedId ?? null },
    });
  }

  @Put()
  async upsert(@Body() dto: AgeoutConfigDto): Promise<AgeoutConfigRecord> {
    const existing = await this.configRepo.findOne({
      where: {
        sourceType: dto.sourceType ?? null,
        feedId: dto.feedId ?? null,
      },
    });

    if (existing) {
      existing.staleThresholdMs = dto.staleThresholdMs;
      existing.ageoutThresholdMs = dto.ageoutThresholdMs;
      return this.configRepo.save(existing);
    }

    const record = new AgeoutConfigRecord();
    record.feedId = dto.feedId ?? null;
    record.sourceType = dto.sourceType ?? null;
    record.staleThresholdMs = dto.staleThresholdMs;
    record.ageoutThresholdMs = dto.ageoutThresholdMs;
    return this.configRepo.save(record);
  }

  @Delete(':id')
  async remove(@Param('id') id: string): Promise<void> {
    await this.configRepo.delete(id);
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx nx test entity-service -- --testPathPattern=ageout-config.controller.spec`
Expected: PASS — all 5 tests

- [ ] **Step 5: Commit**

```bash
git add apps/entity-service/src/entities/ageout-config.controller.ts apps/entity-service/src/entities/ageout-config.controller.spec.ts
git commit -m "feat(entity-service): add ageout config CRUD controller with tests"
```

---

### Task 6: Create ageout polling service with tests

**Files:**
- Create: `apps/entity-service/src/entities/ageout.service.ts`
- Create: `apps/entity-service/src/entities/ageout.service.spec.ts`

- [ ] **Step 1: Write the failing test**

Create `apps/entity-service/src/entities/ageout.service.spec.ts`:

```typescript
import { Test, TestingModule } from '@nestjs/testing';
import { AgeoutService } from './ageout.service';
import { DataSource, Repository } from 'typeorm';
import { getRepositoryToken } from '@nestjs/typeorm';
import { AgeoutConfigRecord } from './ageout-config.entity';
import { ClientKafka } from '@nestjs/microservices';

describe('AgeoutService', () => {
  let service: AgeoutService;
  let dataSource: { query: jest.Mock };
  let configRepo: { find: jest.Mock };
  let kafkaClient: { emit: jest.Mock };

  beforeEach(async () => {
    dataSource = { query: jest.fn() };
    configRepo = { find: jest.fn() };
    kafkaClient = { emit: jest.fn() };

    const module: TestingModule = await Test.createTestingModule({
      providers: [
        AgeoutService,
        { provide: DataSource, useValue: dataSource },
        { provide: getRepositoryToken(AgeoutConfigRecord), useValue: configRepo },
        { provide: 'KAFKA_CLIENT', useValue: kafkaClient },
      ],
    }).compile();

    service = module.get<AgeoutService>(AgeoutService);
  });

  it('should be defined', () => {
    expect(service).toBeDefined();
  });

  describe('processAgeout', () => {
    it('should transition LIVE entities to STALE when past stale threshold', async () => {
      configRepo.find.mockResolvedValue([
        { feedId: null, sourceType: null, staleThresholdMs: 300000, ageoutThresholdMs: 1800000 },
      ]);

      const staleResults = [
        { id: 'e1', entityType: 'AIRCRAFT', source: 'ADS_B', feedId: 'f1', lastSeenAt: new Date() },
      ];
      // First query returns LIVE→STALE transitions, second returns STALE→AGED_OUT transitions
      dataSource.query.mockResolvedValueOnce(staleResults);
      dataSource.query.mockResolvedValueOnce([]);

      await service.processAgeout();

      expect(dataSource.query).toHaveBeenCalledTimes(2);
      expect(kafkaClient.emit).toHaveBeenCalledWith('events.entity.stale', expect.any(Object));
    });

    it('should transition STALE entities to AGED_OUT when past ageout threshold', async () => {
      configRepo.find.mockResolvedValue([
        { feedId: null, sourceType: null, staleThresholdMs: 300000, ageoutThresholdMs: 1800000 },
      ]);

      const agedOutResults = [
        { id: 'e2', entityType: 'VESSEL', source: 'AIS', feedId: 'f2', lastSeenAt: new Date() },
      ];
      dataSource.query.mockResolvedValueOnce([]);
      dataSource.query.mockResolvedValueOnce(agedOutResults);

      await service.processAgeout();

      expect(kafkaClient.emit).toHaveBeenCalledWith('events.entity.agedout', expect.any(Object));
    });

    it('should not process if already running', async () => {
      configRepo.find.mockResolvedValue([]);
      dataSource.query.mockResolvedValue([]);

      // Simulate concurrent call by not awaiting
      const promise1 = service.processAgeout();
      const promise2 = service.processAgeout();

      await Promise.all([promise1, promise2]);

      // Only one cycle should have loaded configs
      expect(configRepo.find).toHaveBeenCalledTimes(1);
    });

    it('should handle empty config gracefully', async () => {
      configRepo.find.mockResolvedValue([]);
      dataSource.query.mockResolvedValue([]);

      await service.processAgeout();

      // Should still run the queries with no configs (uses empty join tables)
      expect(dataSource.query).toHaveBeenCalled();
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test entity-service -- --testPathPattern=ageout.service.spec`
Expected: FAIL — `AgeoutService` not found

- [ ] **Step 3: Write the ageout service**

Create `apps/entity-service/src/entities/ageout.service.ts`:

```typescript
import { Injectable, Logger, Inject, OnModuleInit } from '@nestjs/common';
import { Interval } from '@nestjs/schedule';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository, DataSource } from 'typeorm';
import { ClientKafka } from '@nestjs/microservices';
import { AgeoutConfigRecord } from './ageout-config.entity';
import { KafkaTopics } from '@sentinel/common';

const DEFAULT_SEEDS = [
  { sourceType: 'ADS_B', staleThresholdMs: 60_000, ageoutThresholdMs: 300_000 },
  { sourceType: 'OPENSKY', staleThresholdMs: 60_000, ageoutThresholdMs: 300_000 },
  { sourceType: 'ADSB_LOL', staleThresholdMs: 60_000, ageoutThresholdMs: 300_000 },
  { sourceType: 'AIS', staleThresholdMs: 600_000, ageoutThresholdMs: 1_800_000 },
  { sourceType: 'CELESTRAK', staleThresholdMs: 86_400_000, ageoutThresholdMs: 604_800_000 },
  { sourceType: 'LINK16', staleThresholdMs: 30_000, ageoutThresholdMs: 120_000 },
  { sourceType: 'RADAR', staleThresholdMs: 30_000, ageoutThresholdMs: 120_000 },
  { sourceType: null, staleThresholdMs: 300_000, ageoutThresholdMs: 1_800_000 }, // global default
];

@Injectable()
export class AgeoutService implements OnModuleInit {
  private readonly logger = new Logger(AgeoutService.name);
  private isRunning = false;

  constructor(
    private readonly dataSource: DataSource,
    @InjectRepository(AgeoutConfigRecord)
    private readonly configRepo: Repository<AgeoutConfigRecord>,
    @Inject('KAFKA_CLIENT') private readonly kafkaClient: ClientKafka,
  ) {}

  async onModuleInit(): Promise<void> {
    try {
      // Create the global default unique index (can't be done via @Index decorator)
      await this.dataSource.query(
        `CREATE UNIQUE INDEX IF NOT EXISTS idx_ageout_config_global_default
         ON sentinel.feed_ageout_config ((1))
         WHERE "feedId" IS NULL AND "sourceType" IS NULL`,
      );

      // Seed defaults if table is empty
      const count = await this.configRepo.count();
      if (count === 0) {
        this.logger.log('Seeding default ageout thresholds');
        for (const seed of DEFAULT_SEEDS) {
          const record = new AgeoutConfigRecord();
          record.feedId = null;
          record.sourceType = seed.sourceType;
          record.staleThresholdMs = seed.staleThresholdMs;
          record.ageoutThresholdMs = seed.ageoutThresholdMs;
          await this.configRepo.save(record);
        }
      }
    } catch (error) {
      this.logger.warn(`Ageout init failed: ${error}`);
    }
  }

  @Interval(15_000)
  async processAgeout(): Promise<void> {
    if (this.isRunning) return;
    this.isRunning = true;

    try {
      // LIVE → STALE transition
      const staleTransitions = await this.dataSource.query(`
        WITH candidates AS (
          SELECT e.id,
            COALESCE(
              feed_cfg."staleThresholdMs",
              source_cfg."staleThresholdMs",
              default_cfg."staleThresholdMs"
            ) AS threshold_ms
          FROM sentinel.entities e
          LEFT JOIN sentinel.feed_ageout_config feed_cfg
            ON feed_cfg."feedId" = e."feedId" AND feed_cfg."sourceType" = e.source::varchar
          LEFT JOIN sentinel.feed_ageout_config source_cfg
            ON source_cfg."feedId" IS NULL AND source_cfg."sourceType" = e.source::varchar
          LEFT JOIN sentinel.feed_ageout_config default_cfg
            ON default_cfg."feedId" IS NULL AND default_cfg."sourceType" IS NULL
          WHERE e."ageoutState" = 'LIVE' AND e.deleted = false
            AND e."lastSeenAt" IS NOT NULL
            AND EXTRACT(EPOCH FROM (NOW() - e."lastSeenAt")) * 1000 >
              COALESCE(
                feed_cfg."staleThresholdMs",
                source_cfg."staleThresholdMs",
                default_cfg."staleThresholdMs"
              )
          LIMIT 1000
        )
        UPDATE sentinel.entities SET "ageoutState" = 'STALE'
        FROM candidates c
        WHERE sentinel.entities.id = c.id
        RETURNING sentinel.entities.id, sentinel.entities."entityType",
          sentinel.entities.source, sentinel.entities."feedId",
          sentinel.entities."lastSeenAt", c.threshold_ms
      `);

      for (const row of staleTransitions) {
        this.emitAgeoutEvent(KafkaTopics.ENTITY_STALE, row, 'STALE');
      }

      // STALE → AGED_OUT transition
      const agedOutTransitions = await this.dataSource.query(`
        WITH candidates AS (
          SELECT e.id,
            COALESCE(
              feed_cfg."ageoutThresholdMs",
              source_cfg."ageoutThresholdMs",
              default_cfg."ageoutThresholdMs"
            ) AS threshold_ms
          FROM sentinel.entities e
          LEFT JOIN sentinel.feed_ageout_config feed_cfg
            ON feed_cfg."feedId" = e."feedId" AND feed_cfg."sourceType" = e.source::varchar
          LEFT JOIN sentinel.feed_ageout_config source_cfg
            ON source_cfg."feedId" IS NULL AND source_cfg."sourceType" = e.source::varchar
          LEFT JOIN sentinel.feed_ageout_config default_cfg
            ON default_cfg."feedId" IS NULL AND default_cfg."sourceType" IS NULL
          WHERE e."ageoutState" = 'STALE' AND e.deleted = false
            AND e."lastSeenAt" IS NOT NULL
            AND EXTRACT(EPOCH FROM (NOW() - e."lastSeenAt")) * 1000 >
              COALESCE(
                feed_cfg."ageoutThresholdMs",
                source_cfg."ageoutThresholdMs",
                default_cfg."ageoutThresholdMs"
              )
          LIMIT 1000
        )
        UPDATE sentinel.entities SET "ageoutState" = 'AGED_OUT'
        FROM candidates c
        WHERE sentinel.entities.id = c.id
        RETURNING sentinel.entities.id, sentinel.entities."entityType",
          sentinel.entities.source, sentinel.entities."feedId",
          sentinel.entities."lastSeenAt", c.threshold_ms
      `);

      for (const row of agedOutTransitions) {
        this.emitAgeoutEvent(KafkaTopics.ENTITY_AGED_OUT, row, 'AGED_OUT');
      }

      const total = staleTransitions.length + agedOutTransitions.length;
      if (total > 0) {
        this.logger.log(
          `Ageout cycle: ${staleTransitions.length} → STALE, ${agedOutTransitions.length} → AGED_OUT`,
        );
      }
    } catch (error) {
      this.logger.error(`Ageout processing failed: ${error}`);
    } finally {
      this.isRunning = false;
    }
  }

  private emitAgeoutEvent(
    topic: string,
    row: { id: string; entityType: string; source: string; feedId: string; lastSeenAt: Date; threshold_ms: number },
    ageoutState: string,
  ): void {
    try {
      this.kafkaClient.emit(topic, {
        key: row.id,
        value: JSON.stringify({
          entity_id: row.id,
          entity_type: row.entityType,
          source: row.source,
          feed_id: row.feedId,
          ageout_state: ageoutState,
          last_seen_at: row.lastSeenAt instanceof Date ? row.lastSeenAt.toISOString() : row.lastSeenAt,
          threshold_ms: row.threshold_ms,
          timestamp: new Date().toISOString(),
        }),
        headers: {
          'sentinel-service': 'entity-service',
          'sentinel-timestamp': new Date().toISOString(),
        },
      });
    } catch (error) {
      this.logger.error(`Failed to emit ageout event: ${error}`);
    }
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx nx test entity-service -- --testPathPattern=ageout.service.spec`
Expected: PASS — all 4 tests

- [ ] **Step 5: Commit**

```bash
git add apps/entity-service/src/entities/ageout.service.ts apps/entity-service/src/entities/ageout.service.spec.ts
git commit -m "feat(entity-service): add ageout polling service with state transitions"
```

---

### Task 7: Update entity repository for ageout

**Files:**
- Modify: `apps/entity-service/src/entities/entity.repository.ts`

- [ ] **Step 1: Add ageoutState to ExistingEntityInfo interface**

In `entity.repository.ts`, add to the `ExistingEntityInfo` interface (after `sourceEntityId`):

```typescript
  ageoutState: string;
```

- [ ] **Step 2: Add ageoutState to findBySourceEntityIds select and mapping**

In the `findBySourceEntityIds` method:

1. Add `'e.ageoutState'` to the `.select([...])` array
2. Add `ageoutState: row.e_ageoutState` to the map-building object

- [ ] **Step 3: Add ageoutState reset to bulkUpdatePositions**

In the `bulkUpdatePositions` method, add to the SET clause in the SQL template string (after the `"lastSeenAt" = NOW()` line):

```sql
           "ageoutState" = 'LIVE'
```

- [ ] **Step 4: Add ageout filter to findWithinBoundingBox**

In `findWithinBoundingBox`, add after the existing `.where` clause:

```typescript
      .andWhere('e.ageoutState != :agedOut', { agedOut: 'AGED_OUT' })
```

- [ ] **Step 5: Add ageout filter to findNearby**

In `findNearby`, add after the `.where('e.deleted = :deleted', { deleted: false })` line:

```typescript
      .andWhere('e.ageoutState != :agedOut', { agedOut: 'AGED_OUT' })
```

- [ ] **Step 6: Add ageout filter to findFiltered in entity.service.ts**

In `apps/entity-service/src/entities/entity.service.ts`, in the `findFiltered` method, add after the `.where('e.deleted = :deleted', { deleted: false })` line:

```typescript
      .andWhere('e.ageoutState != :agedOut', { agedOut: 'AGED_OUT' })
```

- [ ] **Step 7: Commit**

```bash
git add apps/entity-service/src/entities/entity.repository.ts apps/entity-service/src/entities/entity.service.ts
git commit -m "feat(entity-service): add ageout state to repository queries and bulk update"
```

---

### Task 8: Add restoration logic to ingest consumer

**Files:**
- Modify: `apps/entity-service/src/entities/ingest.consumer.ts`

- [ ] **Step 1: Import KafkaTopics**

Add to imports in `ingest.consumer.ts`:

```typescript
import { KafkaTopics } from '@sentinel/common';
```

- [ ] **Step 2: Add Kafka client injection**

Add `@Inject('KAFKA_CLIENT')` to the constructor:

```typescript
import { Controller, Logger, OnModuleInit, OnModuleDestroy, Inject } from '@nestjs/common';
import { EventPattern, Payload, ClientKafka } from '@nestjs/microservices';
```

Update constructor:

```typescript
  constructor(
    private readonly entityService: EntityService,
    private readonly entityRepository: EntityRepository,
    private readonly dataSource: DataSource,
    @Inject('KAFKA_CLIENT') private readonly kafkaClient: ClientKafka,
  ) {}
```

- [ ] **Step 3: Store ageoutState on positionUpdates entries**

In the `flushBuffer` method, where `positionUpdates` entries are built from `existing` (the `for (const msg of batch)` loop), add `ageoutState` alongside the other fields copied from `existing`:

```typescript
            positionUpdates.push({
              id: existing.id,
              // ... existing fields ...
              ageoutState: existing.ageoutState,  // add this field
            });
```

Also update the `positionUpdates` type annotation to include `ageoutState: string`.

- [ ] **Step 4: Emit restored events after bulk position update**

In the `flushBuffer` method, replace the existing position update block (around line 198-209) with restoration detection:

```typescript
      // 4. Bulk position update (single SQL — also resets ageoutState to LIVE)
      if (positionUpdates.length > 0) {
        await this.entityRepository.bulkUpdatePositions(positionUpdates);

        for (const u of positionUpdates) {
          // Emit restored event if entity was previously stale/aged-out
          if (u.ageoutState !== 'LIVE') {
            this.emitRestoredEvent(u.id, u.entityType, u.source, u.feedId);
          }

          this.entityService.emitPositionEvent({
            ...u,
            trackEnvironment: u.trackEnvironment ?? undefined,
            platformData: u.platformData ?? undefined,
            circularError: u.circularError ?? undefined,
          });
          await this.entityService.updateRedisGeo(u.id, u.lng, u.lat);
        }
      }
```

Note: `u.feedId` is not currently on positionUpdates entries. Also add `feedId` to the positionUpdates type and copy it from the ingest message: `feedId: msg.feed_id || null`.

- [ ] **Step 4: Add emitRestoredEvent helper**

Add as a private method on `IngestConsumer`:

```typescript
  private emitRestoredEvent(entityId: string, entityType: string, source: string, feedId: string | null): void {
    try {
      this.kafkaClient.emit(KafkaTopics.ENTITY_RESTORED, {
        key: entityId,
        value: JSON.stringify({
          entity_id: entityId,
          entity_type: entityType,
          source,
          feed_id: feedId,
          ageout_state: 'LIVE',
          last_seen_at: new Date().toISOString(),
          threshold_ms: 0,
          timestamp: new Date().toISOString(),
        }),
        headers: {
          'sentinel-service': 'entity-service',
          'sentinel-timestamp': new Date().toISOString(),
        },
      });
      this.logger.log(`Entity restored from ageout: ${entityId}`);
    } catch (error) {
      this.logger.error(`Failed to emit restored event for ${entityId}: ${error}`);
    }
  }
```

- [ ] **Step 5: Commit**

```bash
git add apps/entity-service/src/entities/ingest.consumer.ts
git commit -m "feat(entity-service): emit restored events when stale entities receive updates"
```

---

### Task 9: Register new components in modules

**Files:**
- Modify: `apps/entity-service/src/entities/entity.module.ts`
- Modify: `apps/entity-service/src/app.module.ts`

- [ ] **Step 1: Install @nestjs/schedule**

Run: `npm install @nestjs/schedule` (from workspace root, or add to entity-service's deps)

- [ ] **Step 2: Update app.module.ts**

In `apps/entity-service/src/app.module.ts`, add:

```typescript
import { ScheduleModule } from '@nestjs/schedule';
```

Add `ScheduleModule.forRoot()` to the `imports` array (after `CqrsModule.forRoot()`).

- [ ] **Step 3: Update entity.module.ts**

In `apps/entity-service/src/entities/entity.module.ts`:

1. Add imports:
```typescript
import { AgeoutConfigRecord } from './ageout-config.entity';
import { AgeoutConfigController } from './ageout-config.controller';
import { AgeoutService } from './ageout.service';
```

2. Add `AgeoutConfigRecord` to `TypeOrmModule.forFeature([EntityRecord, ObservationRecord, AgeoutConfigRecord])`
3. Add `AgeoutConfigController` to `controllers: [EntityController, IngestConsumer, AgeoutConfigController]`
4. Add `AgeoutService` to `providers: [EntityService, EntityRepository, ObservationService, ObservationRepository, AgeoutService]`

- [ ] **Step 4: Run existing tests to verify no regressions**

Run: `npx nx test entity-service`
Expected: All existing tests pass

- [ ] **Step 5: Commit**

```bash
git add apps/entity-service/src/app.module.ts apps/entity-service/src/entities/entity.module.ts package.json package-lock.json
git commit -m "feat(entity-service): register ageout service and config in modules"
```

---

### Task 10: Add ageout events to WebSocket gateway

**Files:**
- Modify: `apps/api-gateway/src/modules/gateway/entity.gateway.ts`

- [ ] **Step 1: Add broadcastAgeoutEvent method**

Add to the `EntityGateway` class a method that broadcasts to **all** connected clients (bypassing viewport filtering):

```typescript
  /**
   * Broadcasts an ageout event to ALL connected clients.
   * Ageout events bypass viewport filtering — a stale/aged-out entity must
   * be removed from any client that has it cached, regardless of viewport.
   */
  broadcastAgeoutEvent(eventName: string, payload: unknown): void {
    if (!this.server) return;
    this.server.emit(eventName, payload);
  }
```

- [ ] **Step 2: Add Kafka consumer for ageout events**

The api-gateway has a Kafka consumer service that receives entity events and forwards them to the `EntityGateway`. Find the file with `@EventPattern('events.entity.position')` (or `@MessagePattern`) in the `apps/api-gateway/src/` directory. Add three new event handlers following the same pattern:

```typescript
  @EventPattern('events.entity.stale')
  handleEntityStale(@Payload() message: any): void {
    const payload = typeof message.value === 'string' ? JSON.parse(message.value) : message.value ?? message;
    this.entityGateway.broadcastAgeoutEvent('events.entity.stale', payload);
  }

  @EventPattern('events.entity.agedout')
  handleEntityAgedOut(@Payload() message: any): void {
    const payload = typeof message.value === 'string' ? JSON.parse(message.value) : message.value ?? message;
    this.entityGateway.broadcastAgeoutEvent('events.entity.agedout', payload);
  }

  @EventPattern('events.entity.restored')
  handleEntityRestored(@Payload() message: any): void {
    const payload = typeof message.value === 'string' ? JSON.parse(message.value) : message.value ?? message;
    this.entityGateway.broadcastAgeoutEvent('events.entity.restored', payload);
  }
```

- [ ] **Step 3: Commit**

```bash
git add apps/api-gateway/src/modules/gateway/entity.gateway.ts
git commit -m "feat(api-gateway): forward ageout events via WebSocket to all clients"
```

---

### Task 11: Add ageoutState to frontend Entity model

**Files:**
- Modify: `apps/web/src/app/shared/models/entity.model.ts`

- [ ] **Step 1: Add ageoutState field**

In `entity.model.ts`, add to the `Entity` interface (after `circularError`):

```typescript
  // Ageout state
  ageoutState?: 'LIVE' | 'STALE' | 'AGED_OUT';
```

- [ ] **Step 2: Commit**

```bash
git add apps/web/src/app/shared/models/entity.model.ts
git commit -m "feat(web): add ageoutState to Entity model interface"
```

---

### Task 12: Handle ageout events in WebSocket and entity services

**Files:**
- Modify: `apps/web/src/app/core/services/websocket.service.ts`
- Modify: `apps/web/src/app/core/services/entity.service.ts`

- [ ] **Step 1: Add ageout event listeners to WebSocketService**

In `websocket.service.ts`, add a new subject and observable:

```typescript
  private readonly ageoutSubject = new Subject<{ eventType: string; payload: any }>();
  readonly ageoutEvents$ = this.ageoutSubject.asObservable();
```

In the `connect()` method, add after the `entity:batch` listener:

```typescript
    // Ageout events (broadcast to all clients, not viewport-filtered)
    this.socket.on('events.entity.stale', (payload: any) => {
      this.ageoutSubject.next({ eventType: 'stale', payload });
    });

    this.socket.on('events.entity.agedout', (payload: any) => {
      this.ageoutSubject.next({ eventType: 'agedout', payload });
    });

    this.socket.on('events.entity.restored', (payload: any) => {
      this.ageoutSubject.next({ eventType: 'restored', payload });
    });
```

- [ ] **Step 2: Handle ageout events in EntityService**

In `entity.service.ts`, add a subscription in the constructor (after the existing `wsSubscription`):

```typescript
    // Subscribe to ageout events
    this.ageoutSubscription = this.wsService.ageoutEvents$.subscribe((event) => {
      this.handleAgeoutEvent(event);
    });
```

Add the field declaration:

```typescript
  private readonly ageoutSubscription: Subscription;
```

Add cleanup in `ngOnDestroy`:

```typescript
    this.ageoutSubscription.unsubscribe();
```

Add the handler method:

```typescript
  private handleAgeoutEvent(event: { eventType: string; payload: any }): void {
    const map = this.entitiesSubject.value;
    const entityId = event.payload.entity_id;

    switch (event.eventType) {
      case 'stale': {
        const entity = map.get(entityId);
        if (entity) {
          entity.ageoutState = 'STALE';
          this.entitiesSubject.next(map);
        }
        break;
      }
      case 'agedout': {
        map.delete(entityId);
        this.entitiesSubject.next(map);
        this.entityEvictionsSubject.next([entityId]);
        break;
      }
      case 'restored': {
        const entity = map.get(entityId);
        if (entity) {
          entity.ageoutState = 'LIVE';
          this.entitiesSubject.next(map);
        }
        break;
      }
    }
  }
```

- [ ] **Step 3: Commit**

```bash
git add apps/web/src/app/core/services/websocket.service.ts apps/web/src/app/core/services/entity.service.ts
git commit -m "feat(web): handle ageout WebSocket events in entity service"
```

---

### Task 13: Add staleness visual treatment to map rendering

**Files:**
- Modify: `apps/web/src/app/features/map/map.component.ts`

- [ ] **Step 1: Add staleness opacity calculation**

In `map.component.ts`, add a helper method:

```typescript
  /** Cached ageout configs fetched from backend, refreshed every 60s */
  private ageoutConfigs: Array<{ sourceType: string | null; staleThresholdMs: number }> = [];
  private ageoutConfigTimer: ReturnType<typeof setInterval> | null = null;

  // Call this in ngOnInit (or wherever the component initializes):
  //   this.fetchAgeoutConfigs();
  //   this.ageoutConfigTimer = setInterval(() => this.fetchAgeoutConfigs(), 60_000);
  // Clean up in ngOnDestroy:
  //   if (this.ageoutConfigTimer) clearInterval(this.ageoutConfigTimer);

  private fetchAgeoutConfigs(): void {
    // Fetch from /api/v1/entities/ageout-config
    fetch('/api/v1/entities/ageout-config')
      .then((r) => r.json())
      .then((configs: any[]) => { this.ageoutConfigs = configs; })
      .catch(() => { /* use cached or defaults */ });
  }

  private getStaleThresholdForSource(source: string): number {
    const specific = this.ageoutConfigs.find((c) => c.sourceType === source);
    if (specific) return specific.staleThresholdMs;
    const global = this.ageoutConfigs.find((c) => c.sourceType === null);
    return global?.staleThresholdMs ?? 300_000; // fallback 5min
  }

  /**
   * Determine if an entity should be rendered as stale based on backend state
   * or client-side lastSeenAt calculation for immediate visual feedback.
   */
  private isEntityStale(entity: Entity): boolean {
    if (entity.ageoutState === 'STALE') return true;
    if (entity.ageoutState === 'AGED_OUT') return true;

    // Client-side staleness check for immediate feedback between backend poll cycles
    if (entity.lastSeenAt && entity.source) {
      const elapsed = Date.now() - new Date(entity.lastSeenAt).getTime();
      return elapsed > this.getStaleThresholdForSource(entity.source);
    }

    return false;
  }
```

- [ ] **Step 2: Apply staleness to addOrUpdateCesiumEntity**

In the `addOrUpdateCesiumEntity` method, after the `const cesiumColor = ...` line, add:

```typescript
    const isStale = this.isEntityStale(entity);
    const billboardAlpha = isStale ? 0.4 : 1.0;
    const trailAlpha = isStale ? TRACK_TRAIL_CONFIG.trailOpacity * 0.4 : TRACK_TRAIL_CONFIG.trailOpacity;
```

Then update the rendering to use these alpha values:

**For existing entity updates** (in the `if (existing)` block), add after the billboard rotation update:

```typescript
      if (existing.billboard) {
        existing.billboard.color = (this.isMilitaryAircraft(entity) ? this.Cesium.Color.WHITE : cesiumColor).withAlpha(billboardAlpha);
      }
      if (existing.label) {
        existing.label.fillColor = this.Cesium.Color.WHITE.withAlpha(billboardAlpha);
      }
```

**For new entity creation**, modify the billboard `color` and label `fillColor`:

```typescript
          color: (this.isMilitaryAircraft(entity) ? Cesium.Color.WHITE : cesiumColor).withAlpha(billboardAlpha),
```

```typescript
          fillColor: Cesium.Color.WHITE.withAlpha(billboardAlpha),
```

**For polyline trails**, use `trailAlpha` instead of `TRACK_TRAIL_CONFIG.trailOpacity`:

```typescript
            material: cesiumColor.withAlpha(trailAlpha),
```

- [ ] **Step 3: Skip rendering for AGED_OUT entities**

At the top of `addOrUpdateCesiumEntity`, add after the position null check:

```typescript
    // Don't render aged-out entities (backend filters them from queries,
    // but handle local cache edge case)
    if (entity.ageoutState === 'AGED_OUT') {
      this.removeCesiumEntity(entity.id);
      return;
    }
```

- [ ] **Step 4: Commit**

```bash
git add apps/web/src/app/features/map/map.component.ts
git commit -m "feat(web): add staleness visual treatment to map entity rendering"
```

---

### Task 14: Run all tests and verify

**Files:** None (verification only)

- [ ] **Step 1: Run entity-service tests**

Run: `npx nx test entity-service`
Expected: All tests pass including new ageout tests

- [ ] **Step 2: Run web tests**

Run: `npx nx test web`
Expected: All tests pass

- [ ] **Step 3: Run api-gateway tests**

Run: `npx nx test api-gateway`
Expected: All tests pass

- [ ] **Step 4: Verify build**

Run: `npx nx build entity-service && npx nx build web`
Expected: Clean build with no errors
