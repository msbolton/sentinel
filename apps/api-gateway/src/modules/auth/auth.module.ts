import { Module } from '@nestjs/common';
import { PassportModule } from '@nestjs/passport';
import { JwtStrategy } from './jwt.strategy';
import { JwtAuthGuard } from './jwt-auth.guard';
import { DevAuthGuard } from './dev-auth.guard';

const isProduction = process.env['NODE_ENV'] === 'production';

@Module({
  imports: [
    PassportModule.register({ defaultStrategy: 'jwt' }),
  ],
  providers: [
    JwtStrategy,
    {
      provide: JwtAuthGuard,
      useClass: isProduction ? JwtAuthGuard : DevAuthGuard,
    },
  ],
  exports: [PassportModule, JwtAuthGuard],
})
export class AuthModule {}
