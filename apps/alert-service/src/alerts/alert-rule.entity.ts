import {
  Entity,
  PrimaryGeneratedColumn,
  Column,
  CreateDateColumn,
  UpdateDateColumn,
} from 'typeorm';
import { AlertSeverity, RuleType } from './alert-type.enum';

/**
 * AlertRuleRecord defines a monitoring rule.
 *
 * Config examples by ruleType:
 *
 * GEOFENCE:
 * {
 *   "polygon": [[lng, lat], [lng, lat], ...],
 *   "triggerOn": "ENTRY" | "EXIT" | "BOTH"
 * }
 *
 * SPEED_THRESHOLD:
 * {
 *   "maxSpeedKnots": 30,
 *   "minSpeedKnots": 0,
 *   "sustainedSeconds": 60
 * }
 *
 * PROXIMITY:
 * {
 *   "targetEntityId": "uuid",
 *   "radiusMeters": 500
 * }
 */
@Entity('alert_rules', { schema: 'sentinel' })
export class AlertRuleRecord {
  @PrimaryGeneratedColumn('uuid')
  id: string;

  @Column()
  name: string;

  @Column({ type: 'enum', enum: RuleType })
  ruleType: RuleType;

  @Column({ type: 'jsonb' })
  config: Record<string, unknown>;

  @Column({ type: 'text', array: true, default: '{}' })
  monitoredEntityTypes: string[];

  @Column({ type: 'enum', enum: AlertSeverity, default: AlertSeverity.MEDIUM })
  severity: AlertSeverity;

  @Column({ default: true })
  enabled: boolean;

  @CreateDateColumn()
  createdAt: Date;

  @UpdateDateColumn()
  updatedAt: Date;
}
