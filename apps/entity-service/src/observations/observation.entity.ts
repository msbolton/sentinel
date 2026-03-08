import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  Index,
} from 'typeorm';

@Entity('observations', { schema: 'sentinel' })
@Index('idx_observations_entity_time', ['entityId', 'timestamp'])
@Index('idx_observations_sensor', ['sensorId'], { where: '"sensor_id" IS NOT NULL' })
@Index('idx_observations_feed', ['feedId'], { where: '"feed_id" IS NOT NULL' })
export class ObservationRecord {
  @PrimaryGeneratedColumn('uuid')
  id!: string;

  @Column({ type: 'uuid' })
  entityId!: string;

  @Column({ type: 'varchar', nullable: true })
  sensorId!: string | null;

  @Column({ type: 'uuid', nullable: true })
  feedId!: string | null;

  @Column({ type: 'varchar', nullable: true })
  source!: string | null;

  // Position
  @Column({
    type: 'geometry',
    spatialFeatureType: 'Point',
    srid: 4326,
    nullable: true,
  })
  position!: object | null;

  @Column({ type: 'float', nullable: true })
  altitude!: number | null;

  // Kinematics
  @Column({ type: 'float', nullable: true })
  heading!: number | null;

  @Column({ type: 'float', nullable: true })
  speedKnots!: number | null;

  @Column({ type: 'float', nullable: true })
  course!: number | null;

  @Column({ type: 'float', nullable: true })
  velocityNorth!: number | null;

  @Column({ type: 'float', nullable: true })
  velocityEast!: number | null;

  @Column({ type: 'float', nullable: true })
  velocityUp!: number | null;

  @Column({ type: 'float', nullable: true })
  accelNorth!: number | null;

  @Column({ type: 'float', nullable: true })
  accelEast!: number | null;

  @Column({ type: 'float', nullable: true })
  accelUp!: number | null;

  // Uncertainty
  @Column({ type: 'float', nullable: true })
  circularError!: number | null;

  @Column({ type: 'float', nullable: true })
  semiMajor!: number | null;

  @Column({ type: 'float', nullable: true })
  semiMinor!: number | null;

  @Column({ type: 'float', nullable: true })
  ellipseOrientation!: number | null;

  @Column({ type: 'float', nullable: true })
  altitudeError!: number | null;

  @Column({ type: 'float', nullable: true })
  detectionConfidence!: number | null;

  // Covariance matrices (stored as float arrays)
  @Column({ type: 'float', array: true, nullable: true })
  posCovariance!: number[] | null;

  @Column({ type: 'float', array: true, nullable: true })
  posVelCovariance!: number[] | null;

  @Column({ type: 'float', array: true, nullable: true })
  velCovariance!: number[] | null;

  // Sensor-relative measurements
  @Column({ type: 'float', nullable: true })
  azimuth!: number | null;

  @Column({ type: 'float', nullable: true })
  elevation!: number | null;

  @Column({ type: 'float', nullable: true })
  range!: number | null;

  @Column({ type: 'float', nullable: true })
  azimuthError!: number | null;

  @Column({ type: 'float', nullable: true })
  elevationError!: number | null;

  @Column({ type: 'float', nullable: true })
  rangeError!: number | null;

  // Processing state
  @Column({ type: 'varchar', nullable: true })
  trackProcessingState!: string | null;

  // Raw data
  @Column({ type: 'jsonb', nullable: true })
  rawData!: Record<string, unknown> | null;

  // Timestamp
  @Column({ type: 'timestamptz' })
  timestamp!: Date;
}
