import {
  Entity,
  PrimaryGeneratedColumn,
  Column,
  Index,
  CreateDateColumn,
} from 'typeorm';

/**
 * TrackPoint entity represents a single position observation for a tracked entity.
 * The track_points table is intended to be a TimescaleDB hypertable
 * partitioned by the `timestamp` column for efficient time-series queries.
 *
 * Hypertable creation (run once):
 *   SELECT create_hypertable('sentinel.track_points', 'timestamp');
 */
@Entity('track_points', { schema: 'sentinel' })
@Index('idx_track_points_entity_time', ['entityId', 'timestamp'])
@Index('idx_track_points_timestamp', ['timestamp'])
export class TrackPoint {
  @PrimaryGeneratedColumn('uuid')
  id: string;

  @Column('uuid')
  entityId: string;

  @Column({
    type: 'geometry',
    spatialFeatureType: 'Point',
    srid: 4326,
  })
  position: object;

  @Column({ type: 'float', nullable: true })
  heading: number;

  @Column({ type: 'float', nullable: true })
  speedKnots: number;

  @Column({ type: 'float', nullable: true })
  course: number;

  @Column({ type: 'varchar', nullable: true })
  source: string;

  @Column({ type: 'uuid', nullable: true })
  feedId: string;

  @Column({ type: 'timestamptz' })
  timestamp: Date;
}
