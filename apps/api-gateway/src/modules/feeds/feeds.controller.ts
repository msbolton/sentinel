import {
  Controller,
  Get,
  Put,
  Param,
  Body,
  UseGuards,
  Logger,
} from '@nestjs/common';
import { ApiTags, ApiBearerAuth, ApiOperation, ApiResponse } from '@nestjs/swagger';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { Roles } from '../auth/decorators/roles.decorator';
import { FeedsService, FeedStatus } from './feeds.service';

@ApiTags('feeds')
@ApiBearerAuth('keycloak-jwt')
@UseGuards(JwtAuthGuard)
@Controller('feeds')
export class FeedsController {
  private readonly logger = new Logger(FeedsController.name);

  constructor(private readonly feedsService: FeedsService) {}

  @Get()
  @ApiOperation({ summary: 'List all data feeds and their status' })
  @ApiResponse({ status: 200, description: 'List of data feeds' })
  async listFeeds(): Promise<FeedStatus[]> {
    return this.feedsService.listFeeds();
  }

  @Put(':id')
  @Roles('operator', 'admin')
  @ApiOperation({ summary: 'Toggle a data feed on or off' })
  @ApiResponse({ status: 200, description: 'Updated feed status' })
  @ApiResponse({ status: 404, description: 'Feed not found' })
  async toggleFeed(
    @Param('id') id: string,
    @Body() body: { enabled: boolean },
  ): Promise<FeedStatus> {
    this.logger.log(`Toggling feed ${id} to enabled=${body.enabled}`);
    return this.feedsService.toggleFeed(id, body.enabled);
  }
}
