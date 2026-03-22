import { Controller, Get, Put, Delete, Param, Query, Body } from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository, IsNull } from 'typeorm';
import { AgeoutConfigRecord } from './ageout-config.entity';
import { AgeoutConfigDto } from './dto/ageout-config.dto';

// Auth guards (JwtAuthGuard with sentinel-operator/sentinel-admin roles)
// are enforced at the api-gateway proxy level, consistent with EntityController.
@Controller('entities/ageout-config')
export class AgeoutConfigController {
  constructor(
    @InjectRepository(AgeoutConfigRecord)
    private readonly configRepo: Repository<AgeoutConfigRecord>,
  ) {}

  @Get()
  async findAll(): Promise<AgeoutConfigRecord[]> {
    return this.configRepo.find();
  }

  @Get(':sourceType')
  async findBySourceType(
    @Param('sourceType') sourceType: string,
    @Query('feedId') feedId?: string,
  ): Promise<AgeoutConfigRecord | null> {
    return this.configRepo.findOne({
      where: { sourceType, feedId: feedId ?? IsNull() },
    });
  }

  @Put()
  async upsert(@Body() dto: AgeoutConfigDto): Promise<AgeoutConfigRecord> {
    const existing = await this.configRepo.findOne({
      where: {
        sourceType: dto.sourceType ?? IsNull(),
        feedId: dto.feedId ?? IsNull(),
      },
    });

    if (existing) {
      existing.staleThresholdMs = dto.staleThresholdMs;
      existing.ageoutThresholdMs = dto.ageoutThresholdMs;
      return this.configRepo.save(existing);
    }

    const record = new AgeoutConfigRecord();
    record.feedId = dto.feedId ?? null;
    record.sourceType = dto.sourceType ?? null;
    record.staleThresholdMs = dto.staleThresholdMs;
    record.ageoutThresholdMs = dto.ageoutThresholdMs;
    return this.configRepo.save(record);
  }

  @Delete(':id')
  async remove(@Param('id') id: string): Promise<void> {
    await this.configRepo.delete(id);
  }
}
