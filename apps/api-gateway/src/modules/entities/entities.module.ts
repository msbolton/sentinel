import { Module } from '@nestjs/common';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { ClientsModule, Transport } from '@nestjs/microservices';
import { EntitiesController } from './entities.controller';
import { EntitiesService } from './entities.service';
import { AuthModule } from '../auth/auth.module';

@Module({
  imports: [
    ConfigModule,
    AuthModule,
    ClientsModule.registerAsync([
      {
        name: 'ENTITY_SERVICE',
        imports: [ConfigModule],
        inject: [ConfigService],
        useFactory: (config: ConfigService) => ({
          transport: Transport.KAFKA,
          options: {
            client: {
              clientId: 'sentinel-api-entity-producer',
              brokers: [
                config.get<string>('KAFKA_BROKER', 'localhost:9092'),
              ],
            },
            consumer: {
              groupId: 'sentinel-api-entity',
            },
          },
        }),
      },
    ]),
  ],
  controllers: [EntitiesController],
  providers: [EntitiesService],
  exports: [EntitiesService],
})
export class EntitiesModule {}
