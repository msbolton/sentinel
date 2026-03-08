import {
  Controller,
  Get,
  Post,
  Put,
  Delete,
  Patch,
  Param,
  Body,
  HttpCode,
  UseGuards,
  Logger,
} from '@nestjs/common';
import { ApiTags, ApiBearerAuth, ApiOperation, ApiResponse } from '@nestjs/swagger';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { Roles } from '../auth/decorators/roles.decorator';
import { FeedsService, FeedStatus } from './feeds.service';
import { CreateFeedDto } from './dto/create-feed.dto';
import { UpdateFeedDto } from './dto/update-feed.dto';

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

  @Post()
  @Roles('operator', 'admin')
  @ApiOperation({ summary: 'Create a custom data feed' })
  @ApiResponse({ status: 201, description: 'Feed created' })
  @ApiResponse({ status: 400, description: 'Invalid request' })
  async createFeed(@Body() dto: CreateFeedDto): Promise<FeedStatus> {
    this.logger.log(`Creating custom feed: ${dto.name} (${dto.connector_type}/${dto.format})`);
    return this.feedsService.createFeed(dto);
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

  @Delete(':id')
  @Roles('operator', 'admin')
  @HttpCode(204)
  @ApiOperation({ summary: 'Delete a custom data feed' })
  @ApiResponse({ status: 204, description: 'Feed deleted' })
  @ApiResponse({ status: 403, description: 'Cannot delete built-in feed' })
  @ApiResponse({ status: 404, description: 'Feed not found' })
  async deleteFeed(@Param('id') id: string): Promise<void> {
    this.logger.log(`Deleting feed ${id}`);
    return this.feedsService.deleteFeed(id);
  }

  @Patch(':id')
  @Roles('operator', 'admin')
  @ApiOperation({ summary: 'Update a custom data feed configuration' })
  @ApiResponse({ status: 200, description: 'Updated feed' })
  @ApiResponse({ status: 403, description: 'Cannot update built-in feed' })
  @ApiResponse({ status: 404, description: 'Feed not found' })
  async updateFeed(
    @Param('id') id: string,
    @Body() dto: UpdateFeedDto,
  ): Promise<FeedStatus> {
    this.logger.log(`Updating feed ${id}`);
    return this.feedsService.updateFeed(id, dto);
  }
}
