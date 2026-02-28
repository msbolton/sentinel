import {
  IsOptional,
  IsEnum,
  IsArray,
  IsUUID,
  IsBoolean,
  IsNumber,
  IsString,
  Min,
  Max,
} from 'class-validator';
import { Type, Transform } from 'class-transformer';
import { AlertSeverity, AlertType } from '../alert-type.enum';

export class QueryAlertDto {
  @IsOptional()
  @IsEnum(AlertSeverity)
  severity?: AlertSeverity;

  @IsOptional()
  @IsArray()
  @IsEnum(AlertType, { each: true })
  types?: AlertType[];

  @IsOptional()
  @IsUUID()
  entityId?: string;

  @IsOptional()
  @Transform(({ value }) => value === 'true' || value === true)
  @IsBoolean()
  acknowledged?: boolean;

  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(1)
  page?: number = 1;

  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(1)
  @Max(200)
  pageSize?: number = 20;
}

export class AcknowledgeAlertDto {
  @IsString()
  userId: string;
}
