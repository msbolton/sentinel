import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  CreateDateColumn,
  UpdateDateColumn,
} from 'typeorm';

@Entity('federation_config', { schema: 'sentinel' })
export class FederationConfig {
  @PrimaryGeneratedColumn('uuid')
  id!: string;

  @Column({ type: 'uuid', name: 'instance_id' })
  instanceId!: string;

  @Column({ type: 'varchar', length: 255, name: 'display_name', default: 'Sentinel' })
  displayName!: string;

  @Column({ type: 'varchar', length: 50, name: 'classification_level', default: 'classification-u' })
  classificationLevel!: string;

  @Column({ type: 'boolean', name: 'federation_enabled', default: false })
  federationEnabled: boolean = false;

  @CreateDateColumn({ name: 'created_at' })
  createdAt!: Date;

  @UpdateDateColumn({ name: 'updated_at' })
  updatedAt!: Date;
}
