import { NestFactory } from '@nestjs/core';
import { MicroserviceOptions, Transport } from '@nestjs/microservices';
import { ValidationPipe, Logger } from '@nestjs/common';
import { AppModule } from './app.module';

async function bootstrap() {
  const logger = new Logger('TrackService');

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
        clientId: 'track-service',
        brokers: [process.env['KAFKA_BROKERS'] || 'localhost:9092'],
      },
      consumer: {
        groupId: 'sentinel-track-service',
      },
    },
  });

  await app.startAllMicroservices();
  await app.listen(3002);

  logger.log('Track Service is running on port 3002');
  logger.log('Kafka consumer group: sentinel-track-service');
}

bootstrap();
