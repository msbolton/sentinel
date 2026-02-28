import { IsNumber, IsOptional, Min, Max } from 'class-validator';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';

export class UpdatePositionDto {
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
}
