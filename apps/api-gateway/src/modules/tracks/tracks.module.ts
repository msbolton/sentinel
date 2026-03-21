import { Module } from '@nestjs/common';
import { HttpModule } from '@nestjs/axios';
import { ConfigModule } from '@nestjs/config';
import { AuthModule } from '../auth/auth.module';
import { TracksController } from './tracks.controller';
import { TracksService } from './tracks.service';

@Module({
  imports: [HttpModule, ConfigModule, AuthModule],
  controllers: [TracksController],
  providers: [TracksService],
})
export class TracksModule {}
