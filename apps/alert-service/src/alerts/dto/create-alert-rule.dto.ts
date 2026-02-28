import {
  IsString,
  IsEnum,
  IsObject,
  IsOptional,
  IsArray,
  IsBoolean,
  MaxLength,
} from 'class-validator';
import { AlertSeverity, RuleType } from '../alert-type.enum';

export class CreateAlertRuleDto {
  @IsString()
  @MaxLength(255)
  name: string;

  @IsEnum(RuleType)
  ruleType: RuleType;

  @IsObject()
  config: Record<string, unknown>;

  @IsOptional()
  @IsArray()
  @IsString({ each: true })
  @MaxLength(100, { each: true })
  monitoredEntityTypes?: string[];

  @IsOptional()
  @IsEnum(AlertSeverity)
  severity?: AlertSeverity = AlertSeverity.MEDIUM;

  @IsOptional()
  @IsBoolean()
  enabled?: boolean = true;
}

export class UpdateAlertRuleDto {
  @IsOptional()
  @IsString()
  @MaxLength(255)
  name?: string;

  @IsOptional()
  @IsEnum(RuleType)
  ruleType?: RuleType;

  @IsOptional()
  @IsObject()
  config?: Record<string, unknown>;

  @IsOptional()
  @IsArray()
  @IsString({ each: true })
  @MaxLength(100, { each: true })
  monitoredEntityTypes?: string[];

  @IsOptional()
  @IsEnum(AlertSeverity)
  severity?: AlertSeverity;

  @IsOptional()
  @IsBoolean()
  enabled?: boolean;
}
