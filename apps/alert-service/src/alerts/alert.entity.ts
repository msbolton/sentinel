import {
  Entity,
  PrimaryGeneratedColumn,
  Column,
  CreateDateColumn,
  Index,
} from 'typeorm';
import { AlertType, AlertSeverity } from './alert-type.enum';

@Entity('alerts', { schema: 'sentinel' })
@Index('idx_alerts_entity', ['entityId'])
@Index('idx_alerts_severity', ['severity'])
@Index('idx_alerts_type', ['alertType'])
@Index('idx_alerts_created', ['createdAt'])
export class AlertRecord {
  @PrimaryGeneratedColumn('uuid')
  id: string;

  @Column({ type: 'enum', enum: AlertType })
  alertType: AlertType;

  @Column({ type: 'enum', enum: AlertSeverity })
  severity: AlertSeverity;

  @Column()
  title: string;

  @Column({ type: 'text', nullable: true })
  description: string;

  @Column('uuid')
  entityId: string;

  @Column({ type: 'uuid', array: true, default: '{}' })
  relatedEntityIds: string[];

  @Column({
    type: 'geometry',
    spatialFeatureType: 'Point',
    srid: 4326,
    nullable: true,
  })
  position: object;

  @Column({ type: 'uuid', nullable: true })
  ruleId: string;

  @Column({ type: 'jsonb', default: {} })
  metadata: Record<string, unknown>;

  @CreateDateColumn()
  createdAt: Date;

  @Column({ type: 'timestamptz', nullable: true })
  acknowledgedAt: Date;

  @Column({ nullable: true })
  acknowledgedBy: string;

  @Column({ type: 'timestamptz', nullable: true })
  resolvedAt: Date;
}
