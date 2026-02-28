import {
  IsUUID,
  IsNumber,
  IsOptional,
  IsString,
  IsDateString,
  MaxLength,
  Min,
  Max,
} from 'class-validator';

export class RecordTrackPointDto {
  @IsUUID()
  entityId: string;

  @IsNumber()
  @Min(-90)
  @Max(90)
  latitude: number;

  @IsNumber()
  @Min(-180)
  @Max(180)
  longitude: number;

  @IsOptional()
  @IsNumber()
  @Min(0)
  @Max(360)
  heading?: number;

  @IsOptional()
  @IsNumber()
  @Min(0)
  speedKnots?: number;

  @IsOptional()
  @IsNumber()
  @Min(0)
  @Max(360)
  course?: number;

  @IsOptional()
  @IsString()
  @MaxLength(100)
  source?: string;

  @IsDateString()
  timestamp: string;
}
