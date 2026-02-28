import { Module } from '@nestjs/common';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { TypeOrmModule } from '@nestjs/typeorm';
import { LinkModule } from './links/link.module';

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
        type: 'postgres',
        host: config.get<string>('DB_HOST', 'localhost'),
        port: config.get<number>('DB_PORT', 5432),
        username: config.get<string>('DB_USERNAME', 'sentinel'),
        password: config.get<string>('DB_PASSWORD', 'sentinel'),
        database: config.get<string>('DB_DATABASE', 'sentinel'),
        schema: 'sentinel',
        autoLoadEntities: true,
        synchronize: config.get<string>('NODE_ENV', 'development') === 'development',
      }),
    }),
    LinkModule,
  ],
})
export class AppModule {}
