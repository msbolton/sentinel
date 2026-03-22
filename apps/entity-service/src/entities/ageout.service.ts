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
  { sourceType: null, staleThresholdMs: 300_000, ageoutThresholdMs: 1_800_000 },
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
      await this.dataSource.query(
        `CREATE UNIQUE INDEX IF NOT EXISTS idx_ageout_config_global_default
         ON sentinel.feed_ageout_config ((1))
         WHERE "feedId" IS NULL AND "sourceType" IS NULL`,
      );

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
