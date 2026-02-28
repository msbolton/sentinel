import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  CreateDateColumn,
  UpdateDateColumn,
  Index,
} from 'typeorm';
import { EntityType, EntitySource, Classification } from './enums';

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
export class EntityRecord {
  @PrimaryGeneratedColumn('uuid')
  id: string;

  @Column({ type: 'enum', enum: EntityType })
  entityType: EntityType;

  @Column()
  name: string;

  @Column({ nullable: true })
  description: string;

  @Column({ type: 'enum', enum: EntitySource })
  source: EntitySource;

  @Column({
    type: 'enum',
    enum: Classification,
    default: Classification.UNCLASSIFIED,
  })
  classification: Classification;

  @Column({
    type: 'geometry',
    spatialFeatureType: 'Point',
    srid: 4326,
    nullable: true,
  })
  position: object | null; // GeoJSON Point { type: 'Point', coordinates: [lng, lat] }

  @Column({ type: 'float', nullable: true })
  heading: number | null;

  @Column({ type: 'float', nullable: true })
  speedKnots: number | null;

  @Column({ type: 'float', nullable: true })
  course: number | null;

  @Column({ nullable: true })
  milStd2525dSymbol: string | null;

  @Column({ type: 'jsonb', default: () => "'{}'" })
  metadata: Record<string, unknown>;

  @Column({ type: 'text', array: true, default: () => "ARRAY[]::text[]" })
  affiliations: string[];

  @CreateDateColumn()
  createdAt: Date;

  @UpdateDateColumn()
  updatedAt: Date;

  @Column({ type: 'timestamptz', nullable: true })
  lastSeenAt: Date | null;

  @Column({ type: 'boolean', default: false })
  deleted: boolean;

  @Column({ type: 'timestamptz', nullable: true })
  deletedAt: Date | null;
}
