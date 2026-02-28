import {
  IsString,
  IsNotEmpty,
  IsNumber,
  IsOptional,
  IsEnum,
  IsObject,
  Min,
  Max,
  MaxLength,
} from 'class-validator';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';

/**
 * Supported entity types in the SENTINEL platform.
 */
export enum EntityType {
  AIRCRAFT = 'AIRCRAFT',
  VESSEL = 'VESSEL',
  VEHICLE = 'VEHICLE',
  PERSON = 'PERSON',
  FACILITY = 'FACILITY',
  UNIT = 'UNIT',
  SENSOR = 'SENSOR',
  UNKNOWN = 'UNKNOWN',
}

/**
 * Entity affiliation categories (MIL-STD-2525 compatible).
 */
export enum Affiliation {
  FRIENDLY = 'FRIENDLY',
  HOSTILE = 'HOSTILE',
  NEUTRAL = 'NEUTRAL',
  UNKNOWN = 'UNKNOWN',
  ASSUMED_FRIENDLY = 'ASSUMED_FRIENDLY',
  SUSPECT = 'SUSPECT',
  PENDING = 'PENDING',
}

/**
 * Classification levels for entity data.
 */
export enum ClassificationEnum {
  UNCLASSIFIED = 'UNCLASSIFIED',
  CONFIDENTIAL = 'CONFIDENTIAL',
  SECRET = 'SECRET',
  TOP_SECRET = 'TOP_SECRET',
}

/**
 * DTO for creating a new tracked entity.
 * Validated using class-validator decorators.
 */
export class CreateEntityDto {
  @ApiProperty({
    description: 'Entity type category',
    enum: EntityType,
    example: EntityType.AIRCRAFT,
  })
  @IsEnum(EntityType, {
    message: `entityType must be one of: ${Object.values(EntityType).join(', ')}`,
  })
  entityType!: EntityType;

  @ApiProperty({
    description: 'Human-readable entity name or designator',
    example: 'RAVEN-01',
    maxLength: 255,
  })
  @IsString()
  @IsNotEmpty()
  @MaxLength(255)
  name!: string;

  @ApiProperty({
    description: 'Latitude in decimal degrees (WGS84)',
    example: 38.8977,
    minimum: -90,
    maximum: 90,
  })
  @IsNumber()
  @Min(-90)
  @Max(90)
  latitude!: number;

  @ApiProperty({
    description: 'Longitude in decimal degrees (WGS84)',
    example: -77.0365,
    minimum: -180,
    maximum: 180,
  })
  @IsNumber()
  @Min(-180)
  @Max(180)
  longitude!: number;

  @ApiPropertyOptional({
    description: 'Altitude above mean sea level in meters',
    example: 10000,
  })
  @IsOptional()
  @IsNumber()
  altitude?: number;

  @ApiPropertyOptional({
    description: 'Heading in degrees (0-360, 0 = North)',
    example: 270,
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
    example: 450,
    minimum: 0,
  })
  @IsOptional()
  @IsNumber()
  @Min(0)
  speed?: number;

  @ApiPropertyOptional({
    description: 'Data classification level',
    enum: ClassificationEnum,
    default: ClassificationEnum.UNCLASSIFIED,
  })
  @IsOptional()
  @IsEnum(ClassificationEnum)
  classification?: ClassificationEnum;

  @ApiProperty({
    description: 'Intelligence source or sensor providing this entity',
    example: 'ADS-B',
    maxLength: 100,
  })
  @IsString()
  @IsNotEmpty()
  @MaxLength(100)
  source!: string;

  @ApiPropertyOptional({
    description: 'Entity affiliation (MIL-STD-2525)',
    enum: Affiliation,
  })
  @IsOptional()
  @IsEnum(Affiliation)
  affiliation?: Affiliation;

  @ApiPropertyOptional({
    description: 'Additional metadata key-value pairs',
    example: { tailNumber: 'N12345', callsign: 'RAVEN01' },
  })
  @IsOptional()
  @IsObject()
  metadata?: Record<string, unknown>;
}
