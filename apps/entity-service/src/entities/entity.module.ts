import { Module } from '@nestjs/common';
import { TypeOrmModule } from '@nestjs/typeorm';
import { ClientsModule, Transport } from '@nestjs/microservices';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { EntityRecord } from './entity.entity';
import { EntityRepository } from './entity.repository';
import { EntityService } from './entity.service';
import { EntityController } from './entity.controller';

@Module({
  imports: [
    TypeOrmModule.forFeature([EntityRecord]),

    ClientsModule.registerAsync([
      {
        name: 'KAFKA_CLIENT',
        imports: [ConfigModule],
        inject: [ConfigService],
        useFactory: (config: ConfigService) => ({
          transport: Transport.KAFKA,
          options: {
            client: {
              clientId: 'sentinel-entity-service-producer',
              brokers: [config.get<string>('KAFKA_BROKER', 'localhost:9092')],
            },
            producer: {
              allowAutoTopicCreation: true,
            },
          },
        }),
      },
    ]),
  ],
  controllers: [EntityController],
  providers: [EntityService, EntityRepository],
  exports: [EntityService, EntityRepository],
})
export class EntityModule {}
