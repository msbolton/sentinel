import { Module } from '@nestjs/common';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { ClientsModule, Transport } from '@nestjs/microservices';
import { EntityGateway } from './entity.gateway';
import { ViewportService } from './viewport.service';
import { KafkaConsumerService } from './kafka-consumer.service';

@Module({
  imports: [
    ConfigModule,
    ClientsModule.registerAsync([
      {
        name: 'KAFKA_SERVICE',
        imports: [ConfigModule],
        inject: [ConfigService],
        useFactory: (config: ConfigService) => ({
          transport: Transport.KAFKA,
          options: {
            client: {
              clientId: 'sentinel-gateway-producer',
              brokers: [config.get<string>('KAFKA_BROKER', 'localhost:9092')],
            },
            consumer: {
              groupId: 'sentinel-gateway-consumer',
            },
          },
        }),
      },
    ]),
  ],
  providers: [EntityGateway, ViewportService, KafkaConsumerService],
  exports: [EntityGateway, ViewportService],
})
export class EntityGatewayModule {}
