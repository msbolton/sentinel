import {
  IsUUID,
  IsOptional,
  IsNumber,
  IsString,
  IsDateString,
  IsObject,
  Min,
  Max,
} from 'class-validator';

export class RecordObservationDto {
  @IsUUID()
  entityId!: string;

  @IsOptional() @IsString()
  sensorId?: string;

  @IsOptional() @IsUUID()
  feedId?: string;

  @IsOptional() @IsString()
  source?: string;

  // Position
  @IsOptional() @IsNumber()
  latitude?: number;

  @IsOptional() @IsNumber()
  longitude?: number;

  @IsOptional() @IsNumber()
  altitude?: number;

  // Kinematics
  @IsOptional() @IsNumber()
  heading?: number;

  @IsOptional() @IsNumber()
  speedKnots?: number;

  @IsOptional() @IsNumber()
  course?: number;

  @IsOptional() @IsNumber()
  velocityNorth?: number;

  @IsOptional() @IsNumber()
  velocityEast?: number;

  @IsOptional() @IsNumber()
  velocityUp?: number;

  @IsOptional() @IsNumber()
  accelNorth?: number;

  @IsOptional() @IsNumber()
  accelEast?: number;

  @IsOptional() @IsNumber()
  accelUp?: number;

  // Uncertainty
  @IsOptional() @IsNumber() @Min(0)
  circularError?: number;

  @IsOptional() @IsNumber() @Min(0)
  semiMajor?: number;

  @IsOptional() @IsNumber() @Min(0)
  semiMinor?: number;

  @IsOptional() @IsNumber()
  ellipseOrientation?: number;

  @IsOptional() @IsNumber() @Min(0)
  altitudeError?: number;

  @IsOptional() @IsNumber() @Min(0) @Max(1)
  detectionConfidence?: number;

  // Covariance
  @IsOptional() @IsNumber({}, { each: true })
  posCovariance?: number[];

  @IsOptional() @IsNumber({}, { each: true })
  posVelCovariance?: number[];

  @IsOptional() @IsNumber({}, { each: true })
  velCovariance?: number[];

  // Sensor-relative
  @IsOptional() @IsNumber()
  azimuth?: number;

  @IsOptional() @IsNumber()
  elevation?: number;

  @IsOptional() @IsNumber() @Min(0)
  range?: number;

  @IsOptional() @IsNumber()
  azimuthError?: number;

  @IsOptional() @IsNumber()
  elevationError?: number;

  @IsOptional() @IsNumber()
  rangeError?: number;

  // Processing state
  @IsOptional() @IsString()
  trackProcessingState?: string;

  // Raw data
  @IsOptional() @IsObject()
  rawData?: Record<string, unknown>;

  @IsDateString()
  timestamp!: string;
}
