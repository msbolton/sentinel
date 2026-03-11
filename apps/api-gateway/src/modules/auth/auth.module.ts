import { Module } from '@nestjs/common';
import { APP_INTERCEPTOR } from '@nestjs/core';
import { PassportModule } from '@nestjs/passport';
import { JwtStrategy } from './jwt.strategy';
import { JwtAuthGuard } from './jwt-auth.guard';
import { AuthAuditService } from './auth-audit.service';
import { AuthPropagationInterceptor } from './auth-propagation.interceptor';

@Module({
  imports: [
    PassportModule.register({ defaultStrategy: 'jwt' }),
  ],
  providers: [
    JwtStrategy,
    JwtAuthGuard,
    AuthAuditService,
    {
      provide: APP_INTERCEPTOR,
      useClass: AuthPropagationInterceptor,
    },
  ],
  exports: [PassportModule, JwtAuthGuard, AuthAuditService],
})
export class AuthModule {}
