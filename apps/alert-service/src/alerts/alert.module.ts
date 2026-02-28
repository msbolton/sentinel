import { Module } from '@nestjs/common';
import { TypeOrmModule } from '@nestjs/typeorm';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { ClientsModule, Transport } from '@nestjs/microservices';
import { AlertRecord } from './alert.entity';
import { AlertRuleRecord } from './alert-rule.entity';
import { AlertService } from './alert.service';
import { AlertEvaluatorService } from './alert-evaluator.service';
import { AlertController } from './alert.controller';

@Module({
  imports: [
    TypeOrmModule.forFeature([AlertRecord, AlertRuleRecord]),
    ClientsModule.registerAsync([
      {
        name: 'KAFKA_SERVICE',
        imports: [ConfigModule],
        inject: [ConfigService],
        useFactory: (config: ConfigService) => ({
          transport: Transport.KAFKA,
          options: {
            client: {
              clientId: 'alert-service-producer',
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
  controllers: [AlertController],
  providers: [AlertService, AlertEvaluatorService],
  exports: [AlertService],
})
export class AlertModule {}
