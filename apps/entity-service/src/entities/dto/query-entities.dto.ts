import {
  IsEnum,
  IsOptional,
  IsNumber,
  IsArray,
  Min,
  Max,
} from 'class-validator';
import { Transform } from 'class-transformer';
import { ApiPropertyOptional } from '@nestjs/swagger';
import { EntityType, EntitySource, Classification } from '../enums';

export class QueryEntitiesDto {
  @ApiPropertyOptional({ description: 'Bounding box north latitude', example: 39.0 })
  @IsOptional()
  @IsNumber()
  @Min(-90)
  @Max(90)
  north?: number;

  @ApiPropertyOptional({ description: 'Bounding box south latitude', example: 38.0 })
  @IsOptional()
  @IsNumber()
  @Min(-90)
  @Max(90)
  south?: number;

  @ApiPropertyOptional({ description: 'Bounding box east longitude', example: -76.0 })
  @IsOptional()
  @IsNumber()
  @Min(-180)
  @Max(180)
  east?: number;

  @ApiPropertyOptional({ description: 'Bounding box west longitude', example: -78.0 })
  @IsOptional()
  @IsNumber()
  @Min(-180)
  @Max(180)
  west?: number;

  @ApiPropertyOptional({
    description: 'Filter by entity types',
    enum: EntityType,
    isArray: true,
    example: [EntityType.VESSEL, EntityType.AIRCRAFT],
  })
  @IsOptional()
  @IsArray()
  @IsEnum(EntityType, { each: true })
  @Transform(({ value }) => (Array.isArray(value) ? value : [value]))
  types?: EntityType[];

  @ApiPropertyOptional({
    description: 'Filter by intelligence sources',
    enum: EntitySource,
    isArray: true,
  })
  @IsOptional()
  @IsArray()
  @IsEnum(EntitySource, { each: true })
  @Transform(({ value }) => (Array.isArray(value) ? value : [value]))
  sources?: EntitySource[];

  @ApiPropertyOptional({
    description: 'Maximum classification level to return',
    enum: Classification,
    default: Classification.UNCLASSIFIED,
  })
  @IsOptional()
  @IsEnum(Classification)
  classification?: Classification;

  @ApiPropertyOptional({ description: 'Page number (1-based)', default: 1 })
  @IsOptional()
  @IsNumber()
  @Min(1)
  page?: number = 1;

  @ApiPropertyOptional({ description: 'Results per page', default: 100, maximum: 1000 })
  @IsOptional()
  @IsNumber()
  @Min(1)
  @Max(1000)
  pageSize?: number = 100;
}

export class NearbyQueryDto {
  @ApiPropertyOptional({ description: 'Center latitude', example: 38.8977 })
  @IsNumber()
  @Min(-90)
  @Max(90)
  lat: number;

  @ApiPropertyOptional({ description: 'Center longitude', example: -77.0365 })
  @IsNumber()
  @Min(-180)
  @Max(180)
  lng: number;

  @ApiPropertyOptional({
    description: 'Search radius in meters',
    default: 10000,
    example: 5000,
  })
  @IsOptional()
  @IsNumber()
  @Min(1)
  @Max(500000) // 500km max
  radius?: number = 10000;
}
