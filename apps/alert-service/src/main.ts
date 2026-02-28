import { NestFactory } from '@nestjs/core';
import { MicroserviceOptions, Transport } from '@nestjs/microservices';
import { ValidationPipe, Logger } from '@nestjs/common';
import { AppModule } from './app.module';

async function bootstrap() {
  const logger = new Logger('AlertService');

  const app = await NestFactory.create(AppModule);

  app.useGlobalPipes(
    new ValidationPipe({
      whitelist: true,
      forbidNonWhitelisted: true,
      transform: true,
      transformOptions: { enableImplicitConversion: true },
    }),
  );

  app.enableCors();

  app.connectMicroservice<MicroserviceOptions>({
    transport: Transport.KAFKA,
    options: {
      client: {
        clientId: 'alert-service',
        brokers: [process.env['KAFKA_BROKERS'] || 'localhost:9092'],
      },
      consumer: {
        groupId: 'sentinel-alert-service',
      },
    },
  });

  await app.startAllMicroservices();
  await app.listen(3005);

  logger.log('Alert Service is running on port 3005');
  logger.log('Kafka consumer group: sentinel-alert-service');
}

bootstrap();
