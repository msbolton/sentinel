import { Module } from '@nestjs/common';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { TypeOrmModule } from '@nestjs/typeorm';
import { CqrsModule } from '@nestjs/cqrs';
import { EntityModule } from './entities/entity.module';

@Module({
  imports: [
    ConfigModule.forRoot({
      isGlobal: true,
      envFilePath: ['.env.local', '.env'],
    }),

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
        schema: 'sentinel',
        autoLoadEntities: true,
        synchronize: config.get<string>('NODE_ENV', 'development') === 'development',
        logging: config.get<string>('DB_LOGGING', 'false') === 'true',
        extra: {
          // PostGIS extension is required
          max: config.get<number>('DB_POOL_SIZE', 20),
          idleTimeoutMillis: 30000,
          connectionTimeoutMillis: 5000,
        },
      }),
    }),

    CqrsModule.forRoot(),

    EntityModule,
  ],
})
export class AppModule {}
