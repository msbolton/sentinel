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
