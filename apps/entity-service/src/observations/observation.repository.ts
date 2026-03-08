import { Injectable } from '@nestjs/common';
import { DataSource, Repository } from 'typeorm';
import { ObservationRecord } from './observation.entity';

@Injectable()
export class ObservationRepository extends Repository<ObservationRecord> {
  constructor(private readonly dataSource: DataSource) {
    super(ObservationRecord, dataSource.createEntityManager());
  }

  async findByEntityId(
    entityId: string,
    limit = 100,
    offset = 0,
  ): Promise<[ObservationRecord[], number]> {
    return this.findAndCount({
      where: { entityId },
      order: { timestamp: 'DESC' },
      take: limit,
      skip: offset,
    });
  }

  async findByEntityIdInTimeRange(
    entityId: string,
    startTime: Date,
    endTime: Date,
    limit = 1000,
  ): Promise<ObservationRecord[]> {
    return this.createQueryBuilder('o')
      .where('o.entityId = :entityId', { entityId })
      .andWhere('o.timestamp >= :startTime', { startTime })
      .andWhere('o.timestamp <= :endTime', { endTime })
      .orderBy('o.timestamp', 'ASC')
      .take(limit)
      .getMany();
  }

  async findBySensorId(
    sensorId: string,
    limit = 100,
    offset = 0,
  ): Promise<[ObservationRecord[], number]> {
    return this.findAndCount({
      where: { sensorId },
      order: { timestamp: 'DESC' },
      take: limit,
      skip: offset,
    });
  }
}
