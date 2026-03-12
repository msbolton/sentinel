import { Module } from '@nestjs/common';
import { APP_GUARD, APP_INTERCEPTOR } from '@nestjs/core';
import { PassportModule } from '@nestjs/passport';
import { ConfigService } from '@nestjs/config';
import { ThrottlerModule, ThrottlerGuard } from '@nestjs/throttler';
import { MailerModule } from '@nestjs-modules/mailer';
import { JwtStrategy } from './jwt.strategy';
import { JwtAuthGuard } from './jwt-auth.guard';
import { AuthAuditService } from './auth-audit.service';
import { AuthPropagationInterceptor } from './auth-propagation.interceptor';
import { KeycloakAdminService } from './keycloak-admin.service';
import { RegistrationController } from './registration.controller';

@Module({
  imports: [
    PassportModule.register({ defaultStrategy: 'jwt' }),
    ThrottlerModule.forRoot([{
      ttl: 900000,
      limit: 5,
    }]),
    MailerModule.forRootAsync({
      inject: [ConfigService],
      useFactory: (config: ConfigService) => ({
        transport: {
          host: config.get<string>('MAIL_HOST') ?? 'localhost',
          port: parseInt(config.get<string>('MAIL_PORT') ?? '1025', 10),
          ignoreTLS: true,
        },
        defaults: {
          from: '"SENTINEL Platform" <sentinel@sentinel.local>',
        },
      }),
    }),
  ],
  controllers: [RegistrationController],
  providers: [
    JwtStrategy,
    JwtAuthGuard,
    AuthAuditService,
    KeycloakAdminService,
    {
      provide: APP_GUARD,
      useClass: ThrottlerGuard,
    },
    {
      provide: APP_INTERCEPTOR,
      useClass: AuthPropagationInterceptor,
    },
  ],
  exports: [PassportModule, JwtAuthGuard, AuthAuditService],
})
export class AuthModule {}
