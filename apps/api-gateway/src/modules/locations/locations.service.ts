import { Injectable, Logger, NotFoundException } from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository, ILike } from 'typeorm';
import { LocationRecord } from './location.entity';
import { CreateLocationDto } from './dto/create-location.dto';
import { UpdateLocationDto } from './dto/update-location.dto';
import { QueryLocationsDto } from './dto/query-locations.dto';

@Injectable()
export class LocationsService {
  private readonly logger = new Logger(LocationsService.name);

  constructor(
    @InjectRepository(LocationRecord)
    private readonly locationRepo: Repository<LocationRecord>,
  ) {}

  async findAll(query: QueryLocationsDto): Promise<LocationRecord[]> {
    const where: Record<string, unknown> = {};

    if (query.category) {
      where.category = query.category;
    }
    if (query.search) {
      where.name = ILike(`%${query.search}%`);
    }

    return this.locationRepo.find({
      where,
      order: { name: 'ASC' },
    });
  }

  async findOne(id: string): Promise<LocationRecord> {
    const location = await this.locationRepo.findOneBy({ id });
    if (!location) {
      throw new NotFoundException(`Location ${id} not found`);
    }
    return location;
  }

  async create(dto: CreateLocationDto): Promise<LocationRecord> {
    const location = this.locationRepo.create(dto);
    const saved = await this.locationRepo.save(location);
    this.logger.log(`Created location: ${saved.name} (${saved.id})`);
    return saved;
  }

  async update(id: string, dto: UpdateLocationDto): Promise<LocationRecord> {
    const location = await this.findOne(id);
    Object.assign(location, dto);
    const saved = await this.locationRepo.save(location);
    this.logger.log(`Updated location: ${saved.name} (${saved.id})`);
    return saved;
  }

  async remove(id: string): Promise<void> {
    const location = await this.findOne(id);
    await this.locationRepo.remove(location);
    this.logger.log(`Deleted location: ${location.name} (${id})`);
  }
}
