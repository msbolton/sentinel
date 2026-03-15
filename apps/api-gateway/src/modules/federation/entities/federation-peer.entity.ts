import {
  Entity,
  Column,
  PrimaryColumn,
  CreateDateColumn,
  UpdateDateColumn,
} from 'typeorm';

@Entity('federation_peers', { schema: 'sentinel' })
export class FederationPeer {
  @PrimaryColumn({ type: 'uuid', name: 'instance_id' })
  instanceId!: string;

  @Column({ type: 'varchar', length: 255, name: 'display_name' })
  displayName!: string;

  @Column({ type: 'varchar', length: 512 })
  url!: string;

  @Column({ type: 'varchar', length: 50, name: 'classification_level' })
  classificationLevel!: string;

  @Column({ type: 'varchar', length: 20, default: 'disconnected' })
  status!: string;

  @Column({ type: 'timestamp', name: 'last_seen', nullable: true })
  lastSeen!: Date | null;

  @Column({ type: 'varchar', length: 7, nullable: true })
  color!: string | null;

  @Column({ type: 'boolean', name: 'is_seed', default: false })
  isSeed: boolean = false;

  @CreateDateColumn({ name: 'created_at' })
  createdAt!: Date;

  @UpdateDateColumn({ name: 'updated_at' })
  updatedAt!: Date;
}
