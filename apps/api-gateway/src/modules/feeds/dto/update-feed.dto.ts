import { IsString, IsOptional, IsIn, IsObject, MaxLength } from 'class-validator';
import { ApiPropertyOptional } from '@nestjs/swagger';

export class UpdateFeedDto {
  @ApiPropertyOptional({ description: 'Updated display name', example: 'AIS Feed v2' })
  @IsOptional()
  @IsString()
  @MaxLength(255)
  name?: string;

  @ApiPropertyOptional({
    description: 'Updated message format',
    enum: ['json', 'nmea', 'cot', 'ais', 'adsb', 'link16'],
  })
  @IsOptional()
  @IsIn(['json', 'nmea', 'cot', 'ais', 'adsb', 'link16'])
  format?: string;

  @ApiPropertyOptional({
    description: 'Updated connector-specific configuration',
  })
  @IsOptional()
  @IsObject()
  config?: Record<string, unknown>;
}
