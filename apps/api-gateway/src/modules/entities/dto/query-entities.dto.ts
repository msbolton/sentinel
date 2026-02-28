import {
  IsOptional,
  IsNumber,
  IsString,
  IsArray,
  IsEnum,
  Min,
  Max,
} from 'class-validator';
import { Type, Transform } from 'class-transformer';
import { ApiPropertyOptional } from '@nestjs/swagger';
import { EntityType, ClassificationEnum } from './create-entity.dto';

/**
 * DTO for querying entities with geospatial bounding box,
 * type/source filters, and pagination.
 *
 * All parameters are optional - omitting the bounding box returns
 * all entities (subject to pagination and access controls).
 */
export class QueryEntitiesDto {
  // --- Bounding box ---

  @ApiPropertyOptional({
    description: 'Northern latitude boundary (decimal degrees)',
    example: 39.0,
    minimum: -90,
    maximum: 90,
  })
  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(-90)
  @Max(90)
  north?: number;

  @ApiPropertyOptional({
    description: 'Southern latitude boundary (decimal degrees)',
    example: 38.5,
    minimum: -90,
    maximum: 90,
  })
  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(-90)
  @Max(90)
  south?: number;

  @ApiPropertyOptional({
    description: 'Eastern longitude boundary (decimal degrees)',
    example: -76.5,
    minimum: -180,
    maximum: 180,
  })
  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(-180)
  @Max(180)
  east?: number;

  @ApiPropertyOptional({
    description: 'Western longitude boundary (decimal degrees)',
    example: -77.5,
    minimum: -180,
    maximum: 180,
  })
  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(-180)
  @Max(180)
  west?: number;

  // --- Filters ---

  @ApiPropertyOptional({
    description: 'Filter by entity types (comma-separated or repeated query params)',
    enum: EntityType,
    isArray: true,
    example: [EntityType.AIRCRAFT, EntityType.VESSEL],
  })
  @IsOptional()
  @Transform(({ value }) => {
    if (typeof value === 'string') {
      return value.split(',').map((v: string) => v.trim().toUpperCase());
    }
    if (Array.isArray(value)) {
      return value.map((v: string) => v.trim().toUpperCase());
    }
    return value;
  })
  @IsArray()
  @IsEnum(EntityType, { each: true })
  entityTypes?: EntityType[];

  @ApiPropertyOptional({
    description:
      'Filter by intelligence sources (comma-separated or repeated query params)',
    example: ['ADS-B', 'AIS'],
    isArray: true,
  })
  @IsOptional()
  @Transform(({ value }) => {
    if (typeof value === 'string') {
      return value.split(',').map((v: string) => v.trim());
    }
    return value;
  })
  @IsArray()
  @IsString({ each: true })
  sources?: string[];

  @ApiPropertyOptional({
    description:
      'Maximum classification level to include in results',
    enum: ClassificationEnum,
    example: ClassificationEnum.SECRET,
  })
  @IsOptional()
  @IsEnum(ClassificationEnum)
  classification?: ClassificationEnum;

  // --- Pagination ---

  @ApiPropertyOptional({
    description: 'Page number (1-indexed)',
    default: 1,
    minimum: 1,
  })
  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(1)
  page?: number = 1;

  @ApiPropertyOptional({
    description: 'Number of items per page',
    default: 50,
    minimum: 1,
    maximum: 500,
  })
  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(1)
  @Max(500)
  pageSize?: number = 50;
}
