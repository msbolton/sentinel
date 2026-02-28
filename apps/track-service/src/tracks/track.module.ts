import { Module } from '@nestjs/common';
import { TypeOrmModule } from '@nestjs/typeorm';
import { TrackPoint } from './track-point.entity';
import { TrackService } from './track.service';
import { TrackBatchService } from './track-batch.service';
import { TrackController } from './track.controller';

@Module({
  imports: [TypeOrmModule.forFeature([TrackPoint])],
  controllers: [TrackController],
  providers: [TrackService, TrackBatchService],
  exports: [TrackService],
})
export class TrackModule {}
