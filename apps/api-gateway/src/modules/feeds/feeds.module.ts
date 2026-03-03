import { Module } from '@nestjs/common';
import { HttpModule } from '@nestjs/axios';
import { ConfigModule } from '@nestjs/config';
import { FeedsController } from './feeds.controller';
import { FeedsService } from './feeds.service';

@Module({
  imports: [HttpModule, ConfigModule],
  controllers: [FeedsController],
  providers: [FeedsService],
})
export class FeedsModule {}
