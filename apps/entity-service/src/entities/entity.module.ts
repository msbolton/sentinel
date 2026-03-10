import { Module } from '@nestjs/common';
import { TypeOrmModule } from '@nestjs/typeorm';
import { ClientsModule, Transport } from '@nestjs/microservices';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { EntityRecord } from './entity.entity';
import { EntityRepository } from './entity.repository';
import { EntityService } from './entity.service';
import { EntityController } from './entity.controller';
import { IngestConsumer } from './ingest.consumer';
import { ObservationRecord } from '../observations/observation.entity';
import { ObservationRepository } from '../observations/observation.repository';
import { ObservationService } from '../observations/observation.service';

@Module({
  imports: [
    TypeOrmModule.forFeature([EntityRecord, ObservationRecord]),

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
              brokers: [config.get<string>('KAFKA_BROKERS', 'localhost:9092')],
            },
            producer: {
              allowAutoTopicCreation: true,
            },
          },
        }),
      },
    ]),
  ],
  controllers: [EntityController, IngestConsumer],
  providers: [EntityService, EntityRepository, ObservationService, ObservationRepository],
  exports: [EntityService, EntityRepository, ObservationService],
})
export class EntityModule {}
