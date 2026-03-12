import { Module, MiddlewareConsumer, NestModule } from '@nestjs/common';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { TypeOrmModule } from '@nestjs/typeorm';
import { CqrsModule } from '@nestjs/cqrs';
import { EventEmitterModule } from '@nestjs/event-emitter';
import { EntityGatewayModule } from './modules/gateway/entity-gateway.module';
import { AuthModule } from './modules/auth/auth.module';
import { HealthModule } from './modules/health/health.module';
import { EntitiesModule } from './modules/entities/entities.module';
import { FeedsModule } from './modules/feeds/feeds.module';
import { LocationsModule } from './modules/locations/locations.module';
import { SecurityHeadersMiddleware } from './modules/auth/security-headers.middleware';
import { FederationModule } from './modules/federation/federation.module';

@Module({
  imports: [
    // Global configuration
    ConfigModule.forRoot({
      isGlobal: true,
      envFilePath: ['.env.local', '.env'],
    }),

    // PostgreSQL connection via TypeORM
    TypeOrmModule.forRootAsync({
      imports: [ConfigModule],
      inject: [ConfigService],
      useFactory: (config: ConfigService) => ({
        type: 'postgres' as const,
        host: config.get<string>('DB_HOST', 'localhost'),
        port: config.get<number>('DB_PORT', 5432),
        username: config.get<string>('DB_USERNAME', 'sentinel'),
        password: config.get<string>('DB_PASSWORD', 'sentinel'),
        database: config.get<string>('DB_DATABASE', 'sentinel'),
        autoLoadEntities: true,
        synchronize: config.get<string>('NODE_ENV') !== 'production',
        logging: config.get<string>('NODE_ENV') === 'development',
        ssl:
          config.get<string>('DB_SSL') === 'true'
            ? { rejectUnauthorized: false }
            : false,
      }),
    }),

    // CQRS for command/query separation
    CqrsModule.forRoot(),

    // Event emitter (must be registered once at app level)
    EventEmitterModule.forRoot(),

    // Feature modules
    AuthModule,
    HealthModule,
    EntitiesModule,
    EntityGatewayModule,
    FeedsModule,
    LocationsModule,
    FederationModule,
  ],
})
export class AppModule implements NestModule {
  configure(consumer: MiddlewareConsumer): void {
    consumer.apply(SecurityHeadersMiddleware).forRoutes('*');
  }
}
