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

  // Extended kinematic state
  @Column({ type: 'float', nullable: true })
  altitude: number;

  @Column({ type: 'varchar', nullable: true })
  trackProcessingState: string;

  // Velocity vector (m/s, North-East-Up)
  @Column({ type: 'float', nullable: true })
  velocityNorth: number;

  @Column({ type: 'float', nullable: true })
  velocityEast: number;

  @Column({ type: 'float', nullable: true })
  velocityUp: number;

  // Acceleration vector (m/s², North-East-Up)
  @Column({ type: 'float', nullable: true })
  accelNorth: number;

  @Column({ type: 'float', nullable: true })
  accelEast: number;

  @Column({ type: 'float', nullable: true })
  accelUp: number;

  // Covariance matrices (upper triangle arrays)
  @Column({ type: 'float', array: true, nullable: true })
  posCovariance: number[];

  @Column({ type: 'float', array: true, nullable: true })
  posVelCovariance: number[];

  @Column({ type: 'float', array: true, nullable: true })
  velCovariance: number[];

  // Measurement uncertainty
  @Column({ type: 'float', nullable: true })
  circularError: number;

  @Column({ type: 'float', nullable: true })
  altitudeError: number;

  // Provenance
  @Column({ type: 'varchar', nullable: true })
  sensorId: string;

  @Column({ type: 'timestamptz' })
  timestamp: Date;
}
