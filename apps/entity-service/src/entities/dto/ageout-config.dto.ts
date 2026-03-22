import { IsOptional, IsString, IsInt, IsUUID, Min, Validate, ValidatorConstraint, ValidatorConstraintInterface, ValidationArguments } from 'class-validator';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';

@ValidatorConstraint({ name: 'isGreaterThanStale', async: false })
class IsGreaterThanStaleConstraint implements ValidatorConstraintInterface {
  validate(value: number, args: ValidationArguments): boolean {
    const obj = args.object as AgeoutConfigDto;
    return value > obj.staleThresholdMs;
  }

  defaultMessage(): string {
    return 'ageoutThresholdMs must be greater than staleThresholdMs';
  }
}

export class AgeoutConfigDto {
  @ApiPropertyOptional({ description: 'Feed UUID. Omit for source-type or global default.' })
  @IsOptional()
  @IsUUID()
  feedId?: string;

  @ApiPropertyOptional({ description: 'EntitySource value (e.g. ADS_B, AIS). Omit for global default.' })
  @IsOptional()
  @IsString()
  sourceType?: string;

  @ApiProperty({ description: 'Milliseconds after lastSeenAt before marking STALE', example: 60000 })
  @IsInt()
  @Min(1000)
  staleThresholdMs!: number;

  @ApiProperty({ description: 'Milliseconds after lastSeenAt before marking AGED_OUT', example: 300000 })
  @IsInt()
  @Min(1000)
  @Validate(IsGreaterThanStaleConstraint)
  ageoutThresholdMs!: number;
}

export class AgeoutConfigResponseDto {
  @ApiProperty()
  id!: string;

  @ApiPropertyOptional()
  feedId!: string | null;

  @ApiPropertyOptional()
  sourceType!: string | null;

  @ApiProperty()
  staleThresholdMs!: number;

  @ApiProperty()
  ageoutThresholdMs!: number;

  @ApiProperty()
  createdAt!: string;

  @ApiProperty()
  updatedAt!: string;
}
