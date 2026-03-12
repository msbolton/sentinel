import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  CreateDateColumn,
  UpdateDateColumn,
} from 'typeorm';

@Entity('federation_policies', { schema: 'sentinel' })
export class FederationPolicy {
  @PrimaryGeneratedColumn('uuid')
  id!: string;

  @Column({ type: 'uuid', name: 'peer_instance_id' })
  peerInstanceId!: string;

  @Column({ type: 'jsonb', name: 'entity_types_allowed', default: () => "'[]'" })
  entityTypesAllowed: string[] = [];

  @Column({ type: 'jsonb', name: 'geo_bounds', nullable: true })
  geoBounds!: { north: number; south: number; east: number; west: number } | null;

  @Column({ type: 'boolean', default: true })
  enabled: boolean = true;

  @CreateDateColumn({ name: 'created_at' })
  createdAt!: Date;

  @UpdateDateColumn({ name: 'updated_at' })
  updatedAt!: Date;
}
