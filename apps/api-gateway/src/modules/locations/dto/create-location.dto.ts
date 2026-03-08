import {
  IsString,
  IsNotEmpty,
  IsNumber,
  IsOptional,
  IsEnum,
  IsBoolean,
  Min,
  Max,
  MaxLength,
} from 'class-validator';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';
import { LocationCategory } from '../location-category.enum';

export class CreateLocationDto {
  @ApiProperty({
    description: 'Location name',
    example: 'New York City',
    maxLength: 255,
  })
  @IsString()
  @IsNotEmpty()
  @MaxLength(255)
  name!: string;

  @ApiPropertyOptional({ description: 'Location description' })
  @IsOptional()
  @IsString()
  description?: string;

  @ApiProperty({
    description: 'Latitude in decimal degrees (WGS84)',
    example: 40.7128,
    minimum: -90,
    maximum: 90,
  })
  @IsNumber()
  @Min(-90)
  @Max(90)
  latitude!: number;

  @ApiProperty({
    description: 'Longitude in decimal degrees (WGS84)',
    example: -74.006,
    minimum: -180,
    maximum: 180,
  })
  @IsNumber()
  @Min(-180)
  @Max(180)
  longitude!: number;

  @ApiPropertyOptional({
    description: 'Camera altitude in meters',
    example: 1000,
    default: 1000,
  })
  @IsOptional()
  @IsNumber()
  @Min(0)
  altitude?: number;

  @ApiPropertyOptional({
    description: 'Camera heading in degrees',
    example: 0,
    default: 0,
  })
  @IsOptional()
  @IsNumber()
  heading?: number;

  @ApiPropertyOptional({
    description: 'Camera pitch in degrees',
    example: -45,
    default: -45,
  })
  @IsOptional()
  @IsNumber()
  pitch?: number;

  @ApiPropertyOptional({
    description: 'Camera range in meters',
    example: 2000,
    default: 2000,
  })
  @IsOptional()
  @IsNumber()
  @Min(0)
  range?: number;

  @ApiPropertyOptional({
    description: 'Whether location has Google 3D building tiles',
    default: false,
  })
  @IsOptional()
  @IsBoolean()
  has3dTiles?: boolean;

  @ApiPropertyOptional({
    description: 'Location category',
    enum: LocationCategory,
    default: LocationCategory.CUSTOM,
  })
  @IsOptional()
  @IsEnum(LocationCategory)
  category?: LocationCategory;

  @ApiPropertyOptional({ description: 'User who created the location' })
  @IsOptional()
  @IsString()
  createdBy?: string;
}
