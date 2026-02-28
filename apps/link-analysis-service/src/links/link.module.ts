import { Module } from '@nestjs/common';
import { TypeOrmModule } from '@nestjs/typeorm';
import { LinkRecord } from './link.entity';
import { AgeService } from './age.service';
import { LinkService } from './link.service';
import { LinkController } from './link.controller';

@Module({
  imports: [TypeOrmModule.forFeature([LinkRecord])],
  controllers: [LinkController],
  providers: [AgeService, LinkService],
  exports: [LinkService, AgeService],
})
export class LinkModule {}
