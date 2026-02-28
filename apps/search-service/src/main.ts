import { NestFactory } from '@nestjs/core';
import { MicroserviceOptions, Transport } from '@nestjs/microservices';
import { ValidationPipe, Logger } from '@nestjs/common';
import { AppModule } from './app.module';

async function bootstrap() {
  const logger = new Logger('SearchService');

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
        clientId: 'search-service',
        brokers: [process.env['KAFKA_BROKERS'] || 'localhost:9092'],
      },
      consumer: {
        groupId: 'sentinel-search-service',
      },
    },
  });

  await app.startAllMicroservices();
  await app.listen(3003);

  logger.log('Search Service is running on port 3003');
  logger.log('Kafka consumer group: sentinel-search-service');
}

bootstrap();
