import {
  IsEnum,
  IsString,
  IsOptional,
  IsNumber,
  IsObject,
  IsArray,
  ValidateNested,
  Min,
  Max,
  MaxLength,
} from 'class-validator';
import { Type } from 'class-transformer';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';
import { EntityType, EntitySource, Classification } from '../enums';

export class GeoPointDto {
  @ApiProperty({ description: 'Latitude (WGS84)', example: 38.8977, minimum: -90, maximum: 90 })
  @IsNumber()
  @Min(-90)
  @Max(90)
  lat: number;

  @ApiProperty({ description: 'Longitude (WGS84)', example: -77.0365, minimum: -180, maximum: 180 })
  @IsNumber()
  @Min(-180)
  @Max(180)
  lng: number;
}

export class CreateEntityDto {
  @ApiProperty({ enum: EntityType, description: 'Type of the tracked entity' })
  @IsEnum(EntityType)
  entityType: EntityType;

  @ApiProperty({ description: 'Display name of the entity', example: 'Alpha-7' })
  @IsString()
  @MaxLength(255)
  name: string;

  @ApiPropertyOptional({ description: 'Free-text description', example: 'Cargo vessel heading north' })
  @IsOptional()
  @IsString()
  @MaxLength(2000)
  description?: string;

  @ApiProperty({ enum: EntitySource, description: 'Intelligence source' })
  @IsEnum(EntitySource)
  source: EntitySource;

  @ApiPropertyOptional({
    enum: Classification,
    default: Classification.UNCLASSIFIED,
    description: 'Security classification level',
  })
  @IsOptional()
  @IsEnum(Classification)
  classification?: Classification;

  @ApiPropertyOptional({ description: 'Initial position (lat/lng)', type: GeoPointDto })
  @IsOptional()
  @ValidateNested()
  @Type(() => GeoPointDto)
  position?: GeoPointDto;

  @ApiPropertyOptional({ description: 'Heading in degrees (0-360)', example: 45.0 })
  @IsOptional()
  @IsNumber()
  @Min(0)
  @Max(360)
  heading?: number;

  @ApiPropertyOptional({ description: 'Speed in knots', example: 12.5 })
  @IsOptional()
  @IsNumber()
  @Min(0)
  speedKnots?: number;

  @ApiPropertyOptional({ description: 'Course over ground in degrees (0-360)', example: 90.0 })
  @IsOptional()
  @IsNumber()
  @Min(0)
  @Max(360)
  course?: number;

  @ApiPropertyOptional({
    description: 'MIL-STD-2525D symbol identification code',
    example: 'SFGPUCII---AA--',
  })
  @IsOptional()
  @IsString()
  @MaxLength(30)
  milStd2525dSymbol?: string;

  @ApiPropertyOptional({
    description: 'Arbitrary key-value metadata',
    example: { mmsi: '123456789', callsign: 'VICTOR' },
  })
  @IsOptional()
  @IsObject()
  metadata?: Record<string, unknown>;

  @ApiPropertyOptional({
    description: 'Affiliation tags',
    example: ['NATO', 'FVEY'],
    type: [String],
  })
  @IsOptional()
  @IsArray()
  @IsString({ each: true })
  affiliations?: string[];
}
