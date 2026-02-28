import {
  IsUUID,
  IsEnum,
  IsNumber,
  IsOptional,
  IsString,
  IsArray,
  IsDateString,
  IsObject,
  MaxLength,
  Min,
  Max,
} from 'class-validator';
import { LinkType } from '../link-type.enum';

export class CreateLinkDto {
  @IsUUID()
  sourceEntityId: string;

  @IsUUID()
  targetEntityId: string;

  @IsEnum(LinkType)
  linkType: LinkType;

  @IsOptional()
  @IsNumber()
  @Min(0)
  @Max(1)
  confidence?: number = 0.5;

  @IsOptional()
  @IsString()
  @MaxLength(2000)
  description?: string;

  @IsOptional()
  @IsArray()
  @IsString({ each: true })
  @MaxLength(2000, { each: true })
  evidence?: string[];

  @IsOptional()
  @IsDateString()
  firstObserved?: string;

  @IsOptional()
  @IsDateString()
  lastObserved?: string;

  @IsOptional()
  @IsObject()
  metadata?: Record<string, string>;
}
