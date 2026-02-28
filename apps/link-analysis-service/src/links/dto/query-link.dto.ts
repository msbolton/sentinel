import {
  IsUUID,
  IsOptional,
  IsEnum,
  IsNumber,
  IsArray,
  Min,
  Max,
} from 'class-validator';
import { Type } from 'class-transformer';
import { LinkType } from '../link-type.enum';

export class QueryLinkDto {
  @IsUUID()
  entityId: string;

  @IsOptional()
  @IsArray()
  @IsEnum(LinkType, { each: true })
  types?: LinkType[];

  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(0)
  @Max(1)
  minConfidence?: number;
}

export class QueryGraphDto {
  @IsUUID()
  centerId: string;

  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(1)
  @Max(10)
  maxDepth?: number = 3;

  @IsOptional()
  @IsArray()
  @IsEnum(LinkType, { each: true })
  types?: LinkType[];

  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(0)
  @Max(1)
  minConfidence?: number;
}

export class ShortestPathDto {
  @IsUUID()
  from: string;

  @IsUUID()
  to: string;
}
