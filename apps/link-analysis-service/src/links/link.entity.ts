import {
  Entity,
  PrimaryGeneratedColumn,
  Column,
  CreateDateColumn,
  Index,
} from 'typeorm';
import { LinkType } from './link-type.enum';

@Entity('links', { schema: 'sentinel' })
@Index('idx_links_source_entity', ['sourceEntityId'])
@Index('idx_links_target_entity', ['targetEntityId'])
@Index('idx_links_type', ['linkType'])
export class LinkRecord {
  @PrimaryGeneratedColumn('uuid')
  id: string;

  @Column('uuid')
  sourceEntityId: string;

  @Column('uuid')
  targetEntityId: string;

  @Column({ type: 'enum', enum: LinkType })
  linkType: LinkType;

  @Column({ type: 'float', default: 0.5 })
  confidence: number;

  @Column({ nullable: true })
  description: string;

  @Column({ type: 'text', array: true, default: '{}' })
  evidence: string[];

  @Column({ type: 'timestamptz', nullable: true })
  firstObserved: Date;

  @Column({ type: 'timestamptz', nullable: true })
  lastObserved: Date;

  @Column({ type: 'jsonb', default: {} })
  metadata: Record<string, string>;

  @CreateDateColumn()
  createdAt: Date;
}
