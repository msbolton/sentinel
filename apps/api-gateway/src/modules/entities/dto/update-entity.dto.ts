import {
  IsString,
  IsNumber,
  IsOptional,
  IsEnum,
  IsObject,
  Min,
  Max,
  MaxLength,
} from 'class-validator';
import { ApiPropertyOptional } from '@nestjs/swagger';
import {
  EntityType,
  Affiliation,
  Classification,
} from './create-entity.dto';

/**
 * DTO for updating an existing entity.
 * All fields are optional - only provided fields will be updated.
 */
export class UpdateEntityDto {
  @ApiPropertyOptional({
    description: 'Entity type category',
    enum: EntityType,
  })
  @IsOptional()
  @IsEnum(EntityType, {
    message: `entityType must be one of: ${Object.values(EntityType).join(', ')}`,
  })
  entityType?: EntityType;

  @ApiPropertyOptional({
    description: 'Human-readable entity name or designator',
    maxLength: 255,
  })
  @IsOptional()
  @IsString()
  @MaxLength(255)
  name?: string;

  @ApiPropertyOptional({
    description: 'Latitude in decimal degrees (WGS84)',
    minimum: -90,
    maximum: 90,
  })
  @IsOptional()
  @IsNumber()
  @Min(-90)
  @Max(90)
  latitude?: number;

  @ApiPropertyOptional({
    description: 'Longitude in decimal degrees (WGS84)',
    minimum: -180,
    maximum: 180,
  })
  @IsOptional()
  @IsNumber()
  @Min(-180)
  @Max(180)
  longitude?: number;

  @ApiPropertyOptional({
    description: 'Altitude above mean sea level in meters',
  })
  @IsOptional()
  @IsNumber()
  altitude?: number;

  @ApiPropertyOptional({
    description: 'Heading in degrees (0-360, 0 = North)',
    minimum: 0,
    maximum: 360,
  })
  @IsOptional()
  @IsNumber()
  @Min(0)
  @Max(360)
  heading?: number;

  @ApiPropertyOptional({
    description: 'Speed in knots',
    minimum: 0,
  })
  @IsOptional()
  @IsNumber()
  @Min(0)
  speed?: number;

  @ApiPropertyOptional({
    description: 'Data classification level',
    enum: Classification,
  })
  @IsOptional()
  @IsEnum(Classification)
  classification?: Classification;

  @ApiPropertyOptional({
    description: 'Intelligence source or sensor',
    maxLength: 100,
  })
  @IsOptional()
  @IsString()
  @MaxLength(100)
  source?: string;

  @ApiPropertyOptional({
    description: 'Entity affiliation (MIL-STD-2525)',
    enum: Affiliation,
  })
  @IsOptional()
  @IsEnum(Affiliation)
  affiliation?: Affiliation;

  @ApiPropertyOptional({
    description: 'Additional metadata key-value pairs',
  })
  @IsOptional()
  @IsObject()
  metadata?: Record<string, unknown>;
}
