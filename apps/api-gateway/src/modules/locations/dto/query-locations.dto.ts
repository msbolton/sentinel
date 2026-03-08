import { IsOptional, IsEnum, IsString } from 'class-validator';
import { ApiPropertyOptional } from '@nestjs/swagger';
import { LocationCategory } from '../location-category.enum';

export class QueryLocationsDto {
  @ApiPropertyOptional({
    description: 'Filter by category',
    enum: LocationCategory,
  })
  @IsOptional()
  @IsEnum(LocationCategory)
  category?: LocationCategory;

  @ApiPropertyOptional({
    description: 'Search by name (case-insensitive partial match)',
    example: 'new york',
  })
  @IsOptional()
  @IsString()
  search?: string;
}
