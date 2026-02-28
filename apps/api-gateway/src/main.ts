import { NestFactory } from '@nestjs/core';
import { ValidationPipe, Logger } from '@nestjs/common';
import { MicroserviceOptions, Transport } from '@nestjs/microservices';
import { DocumentBuilder, SwaggerModule } from '@nestjs/swagger';
import { AppModule } from './app.module';

async function bootstrap(): Promise<void> {
  const logger = new Logger('Bootstrap');

  const app = await NestFactory.create(AppModule, {
    logger: ['error', 'warn', 'log', 'debug', 'verbose'],
  });

  // CORS
  app.enableCors({
    origin: process.env['CORS_ORIGIN'] ?? 'http://localhost:4200',
    credentials: true,
  });

  // Global prefix
  app.setGlobalPrefix('api/v1');

  // Global validation pipe
  app.useGlobalPipes(
    new ValidationPipe({
      whitelist: true,
      forbidNonWhitelisted: true,
      transform: true,
      transformOptions: {
        enableImplicitConversion: true,
      },
    }),
  );

  // Swagger documentation
  const swaggerConfig = new DocumentBuilder()
    .setTitle('SENTINEL API')
    .setDescription(
      'SENTINEL Geospatial Intelligence Platform - REST API documentation',
    )
    .setVersion('1.0')
    .addBearerAuth(
      {
        type: 'http',
        scheme: 'bearer',
        bearerFormat: 'JWT',
        description: 'Enter Keycloak JWT token',
      },
      'keycloak-jwt',
    )
    .addTag('entities', 'Entity management and geospatial queries')
    .addTag('health', 'Service health and readiness checks')
    .build();

  const document = SwaggerModule.createDocument(app, swaggerConfig);
  SwaggerModule.setup('api/docs', app, document);

  // Kafka microservice transport
  const kafkaBroker = process.env['KAFKA_BROKER'] ?? 'localhost:9092';
  app.connectMicroservice<MicroserviceOptions>({
    transport: Transport.KAFKA,
    options: {
      client: {
        clientId: 'sentinel-api-gateway',
        brokers: [kafkaBroker],
      },
      consumer: {
        groupId: 'sentinel-api-gateway',
        sessionTimeout: 30000,
        heartbeatInterval: 10000,
      },
      producer: {
        allowAutoTopicCreation: true,
      },
    },
  });

  await app.startAllMicroservices();

  const port = process.env['PORT'] ?? 3000;
  await app.listen(port);

  logger.log(`SENTINEL API Gateway running on http://localhost:${port}`);
  logger.log(`Swagger docs available at http://localhost:${port}/api/docs`);
  logger.log(`Kafka consumer connected to ${kafkaBroker}`);
}

bootstrap();
