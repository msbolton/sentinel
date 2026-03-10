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

  @IsOptional()
  @IsNumber()
  altitude?: number;

  @IsOptional()
  @IsString()
  trackProcessingState?: string;

  @IsOptional()
  @IsNumber()
  velocityNorth?: number;

  @IsOptional()
  @IsNumber()
  velocityEast?: number;

  @IsOptional()
  @IsNumber()
  velocityUp?: number;

  @IsOptional()
  @IsNumber()
  accelNorth?: number;

  @IsOptional()
  @IsNumber()
  accelEast?: number;

  @IsOptional()
  @IsNumber()
  accelUp?: number;

  @IsOptional()
  @IsNumber({}, { each: true })
  posCovariance?: number[];

  @IsOptional()
  @IsNumber({}, { each: true })
  posVelCovariance?: number[];

  @IsOptional()
  @IsNumber({}, { each: true })
  velCovariance?: number[];

  @IsOptional()
  @IsNumber()
  @Min(0)
  circularError?: number;

  @IsOptional()
  @IsNumber()
  @Min(0)
  altitudeError?: number;

  @IsOptional()
  @IsString()
  sensorId?: string;

  @IsDateString()
  timestamp: string;
}
