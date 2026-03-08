import {
  Entity,
  Column,
  PrimaryGeneratedColumn,
  CreateDateColumn,
  UpdateDateColumn,
} from 'typeorm';
import { LocationCategory } from './location-category.enum';

@Entity('locations', { schema: 'sentinel' })
export class LocationRecord {
  @PrimaryGeneratedColumn('uuid')
  id!: string;

  @Column({ type: 'varchar', length: 255 })
  name!: string;

  @Column({ type: 'text', nullable: true })
  description!: string | null;

  @Column({ type: 'float' })
  latitude!: number;

  @Column({ type: 'float' })
  longitude!: number;

  @Column({ type: 'float', default: 1000 })
  altitude!: number;

  @Column({ type: 'float', default: 0 })
  heading!: number;

  @Column({ type: 'float', default: -45 })
  pitch!: number;

  @Column({ type: 'float', default: 2000 })
  range!: number;

  @Column({ type: 'boolean', default: false })
  has3dTiles!: boolean;

  @Column({
    type: 'enum',
    enum: LocationCategory,
    default: LocationCategory.CUSTOM,
  })
  category!: LocationCategory;

  @Column({ type: 'varchar', length: 255, nullable: true })
  createdBy!: string | null;

  @CreateDateColumn()
  createdAt!: Date;

  @UpdateDateColumn()
  updatedAt!: Date;
}
