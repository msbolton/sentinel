import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  CreateDateColumn,
  UpdateDateColumn,
  Index,
} from 'typeorm';
import {
  EntityType,
  EntitySource,
  Classification,
  TrackEnvironment,
  TrackProcessingState,
  OperationalStatus,
  DamageAssessment,
  CharacterizationState,
  Affiliation,
  AgeoutState,
} from './enums';

/**
 * Core entity record for the SENTINEL geospatial intelligence platform.
 * Represents any tracked object (person, vehicle, vessel, aircraft, etc.)
 * with PostGIS-backed geospatial position data.
 */
@Entity('entities', { schema: 'sentinel' })
@Index('idx_entities_entity_type', ['entityType'])
@Index('idx_entities_source', ['source'])
@Index('idx_entities_classification', ['classification'])
@Index('idx_entities_last_seen_at', ['lastSeenAt'])
@Index('idx_entities_position', ['position'], { spatial: true })
@Index('idx_entities_source_entity_id', { synchronize: false })
@Index('idx_entities_feed_id', ['feedId'])
@Index('idx_entities_affiliation', ['affiliation'])
@Index('idx_entities_track_environment', ['trackEnvironment'])
@Index('idx_entities_operational_status', ['operationalStatus'])
@Index('idx_entities_country_of_origin', ['countryOfOrigin'])
@Index('idx_entities_source_entity_id_col', ['sourceEntityId'])
@Index('idx_entities_ageout_state', ['ageoutState'])
export class EntityRecord {
  @PrimaryGeneratedColumn('uuid')
  id!: string;

  @Column({ type: 'enum', enum: EntityType })
  entityType!: EntityType;

  @Column()
  name!: string;

  @Column({ nullable: true })
  description!: string;

  @Column({ type: 'enum', enum: EntitySource })
  source!: EntitySource;

  @Column({
    type: 'enum',
    enum: Classification,
    default: Classification.UNCLASSIFIED,
  })
  classification!: Classification;

  @Column({ type: 'uuid', nullable: true })
  feedId!: string | null;

  @Column({
    type: 'geometry',
    spatialFeatureType: 'Point',
    srid: 4326,
    nullable: true,
  })
  position!: object | null; // GeoJSON Point { type: 'Point', coordinates: [lng, lat] }

  @Column({ type: 'float', nullable: true })
  heading!: number | null;

  @Column({ type: 'float', nullable: true })
  speedKnots!: number | null;

  @Column({ type: 'float', nullable: true })
  course!: number | null;

  @Column({ type: 'float', nullable: true })
  altitude!: number | null;

  @Column({ type: 'varchar', nullable: true })
  milStd2525dSymbol!: string | null;

  @Column({ type: 'jsonb', default: () => "'{}'" })
  metadata!: Record<string, unknown>;

  @Column({ type: 'text', array: true, default: () => "ARRAY[]::text[]" })
  affiliations!: string[];

  @Column({ type: 'varchar', default: 'UNKNOWN' })
  affiliation!: string;

  @Column({ type: 'smallint', default: 0 })
  identityConfidence!: number;

  @Column({ type: 'varchar', default: 'UNCHARACTERIZED' })
  characterization!: string;

  // Track context
  @Column({ type: 'varchar', default: 'UNKNOWN' })
  trackEnvironment!: string;

  @Column({ type: 'varchar', default: 'LIVE' })
  trackProcessingState!: string;

  // Orientation (full 3-axis)
  @Column({ type: 'float', nullable: true })
  pitch!: number | null;

  @Column({ type: 'float', nullable: true })
  roll!: number | null;

  // Operational status
  @Column({ type: 'varchar', default: 'UNKNOWN' })
  operationalStatus!: string;

  @Column({ type: 'varchar', default: 'UNKNOWN' })
  damageAssessment!: string;

  @Column({ type: 'smallint', default: 0 })
  damageConfidence!: number;

  // Physical characteristics
  @Column({ type: 'float', nullable: true })
  dimensionLength!: number | null;

  @Column({ type: 'float', nullable: true })
  dimensionWidth!: number | null;

  @Column({ type: 'float', nullable: true })
  dimensionHeight!: number | null;

  @Column({ type: 'varchar', length: 2, nullable: true })
  countryOfOrigin!: string | null;

  // Kinematics JSONB (velocity, acceleration, covariance matrices)
  @Column({ type: 'jsonb', default: () => "'{}'" })
  kinematics!: Record<string, unknown>;

  // Protocol-specific typed JSONB
  @Column({ type: 'jsonb', default: () => "'{}'" })
  platformData!: Record<string, unknown>;

  // Measurement quality
  @Column({ type: 'float', nullable: true })
  circularError!: number | null;

  @Column({ type: 'varchar', nullable: true })
  lastObservationSource!: string | null;

  // Promoted from metadata JSONB
  @Column({ type: 'varchar', nullable: true })
  sourceEntityId!: string | null;

  @CreateDateColumn()
  createdAt!: Date;

  @UpdateDateColumn()
  updatedAt!: Date;

  @Column({ type: 'timestamptz', nullable: true })
  lastSeenAt!: Date | null;

  @Column({ type: 'boolean', default: false })
  deleted!: boolean;

  @Column({ type: 'timestamptz', nullable: true })
  deletedAt!: Date | null;

  @Column({ type: 'varchar', default: 'LIVE' })
  ageoutState!: string;
}
