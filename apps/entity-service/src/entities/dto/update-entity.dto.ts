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
import { ApiPropertyOptional } from '@nestjs/swagger';
import { EntityType, EntitySource, Classification } from '../enums';
import { GeoPointDto } from './create-entity.dto';

export class UpdateEntityDto {
  @ApiPropertyOptional({ enum: EntityType, description: 'Type of the tracked entity' })
  @IsOptional()
  @IsEnum(EntityType)
  entityType?: EntityType;

  @ApiPropertyOptional({ description: 'Display name of the entity' })
  @IsOptional()
  @IsString()
  @MaxLength(255)
  name?: string;

  @ApiPropertyOptional({ description: 'Free-text description' })
  @IsOptional()
  @IsString()
  @MaxLength(2000)
  description?: string;

  @ApiPropertyOptional({ enum: EntitySource, description: 'Intelligence source' })
  @IsOptional()
  @IsEnum(EntitySource)
  source?: EntitySource;

  @ApiPropertyOptional({ enum: Classification, description: 'Security classification level' })
  @IsOptional()
  @IsEnum(Classification)
  classification?: Classification;

  @ApiPropertyOptional({ description: 'Position (lat/lng)', type: GeoPointDto })
  @IsOptional()
  @ValidateNested()
  @Type(() => GeoPointDto)
  position?: GeoPointDto;

  @ApiPropertyOptional({ description: 'Heading in degrees (0-360)' })
  @IsOptional()
  @IsNumber()
  @Min(0)
  @Max(360)
  heading?: number;

  @ApiPropertyOptional({ description: 'Speed in knots' })
  @IsOptional()
  @IsNumber()
  @Min(0)
  speedKnots?: number;

  @ApiPropertyOptional({ description: 'Course over ground in degrees (0-360)' })
  @IsOptional()
  @IsNumber()
  @Min(0)
  @Max(360)
  course?: number;

  @ApiPropertyOptional({ description: 'MIL-STD-2525D symbol identification code' })
  @IsOptional()
  @IsString()
  @MaxLength(30)
  milStd2525dSymbol?: string;

  @ApiPropertyOptional({ description: 'Arbitrary key-value metadata' })
  @IsOptional()
  @IsObject()
  metadata?: Record<string, unknown>;

  @ApiPropertyOptional({ description: 'Affiliation tags', type: [String] })
  @IsOptional()
  @IsArray()
  @IsString({ each: true })
  affiliations?: string[];
}
