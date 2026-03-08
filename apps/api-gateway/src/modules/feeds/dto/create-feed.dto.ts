import { IsString, IsNotEmpty, IsIn, IsObject, MaxLength } from 'class-validator';
import { ApiProperty } from '@nestjs/swagger';

export class CreateFeedDto {
  @ApiProperty({ description: 'Display name for the feed', example: 'AIS Maritime Feed' })
  @IsString()
  @IsNotEmpty()
  @MaxLength(255)
  name!: string;

  @ApiProperty({
    description: 'Connector protocol',
    enum: ['mqtt', 'stomp', 'tcp'],
    example: 'mqtt',
  })
  @IsIn(['mqtt', 'stomp', 'tcp'])
  connector_type!: string;

  @ApiProperty({
    description: 'Message format',
    enum: ['json', 'nmea', 'cot', 'ais', 'adsb', 'link16'],
    example: 'json',
  })
  @IsIn(['json', 'nmea', 'cot', 'ais', 'adsb', 'link16'])
  format!: string;

  @ApiProperty({
    description: 'Connector-specific configuration',
    example: { broker_url: 'tcp://localhost:1883', topics: ['sensors/#'], qos: 1 },
  })
  @IsObject()
  config!: Record<string, unknown>;
}
