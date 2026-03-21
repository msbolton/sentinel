import {
  Controller,
  Get,
  Post,
  Param,
  Query,
  Body,
  ParseUUIDPipe,
  HttpCode,
  HttpStatus,
} from '@nestjs/common';
import { MessagePattern, Payload } from '@nestjs/microservices';
import { TrackService } from './track.service';
import { QueryTrackDto, ReplayTrackDto } from './dto/query-track.dto';

@Controller('tracks')
export class TrackController {
  constructor(private readonly trackService: TrackService) {}

  /**
   * GET /tracks/:entityId
   * Query track history with optional time range, point limit, and simplification.
   */
  @Get(':entityId')
  async getTrackHistory(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: QueryTrackDto,
  ) {
    const startTime = query.startTime ? new Date(query.startTime) : undefined;
    const endTime = query.endTime ? new Date(query.endTime) : undefined;

    return this.trackService.getHistory(
      entityId,
      startTime,
      endTime,
      query.maxPoints,
      query.simplify,
    );
  }

  /**
   * GET /tracks/:entityId/latest
   * Get the most recent track point for a single entity.
   */
  @Get(':entityId/latest')
  async getLatestPosition(
    @Param('entityId', ParseUUIDPipe) entityId: string,
  ) {
    const results = await this.trackService.getLatestPositions([entityId]);
    if (results.length === 0) {
      return null;
    }
    return results[0];
  }

  /**
   * GET /tracks/:entityId/segments
   * Get track segments (gaps > 30 min create new segments).
   */
  @Get(':entityId/segments')
  async getTrackSegments(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: QueryTrackDto,
  ) {
    const startTime = query.startTime ? new Date(query.startTime) : undefined;
    const endTime = query.endTime ? new Date(query.endTime) : undefined;

    return this.trackService.getSegments(entityId, startTime, endTime);
  }

  /**
   * POST /tracks/:entityId/replay
   * Initiate a track replay. Returns a WebSocket channel identifier
   * that clients can subscribe to for real-time replay playback.
   */
  @Post(':entityId/replay')
  @HttpCode(HttpStatus.OK)
  async replayTrack(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Body() dto: ReplayTrackDto,
  ) {
    const startTime = new Date(dto.startTime);
    const endTime = new Date(dto.endTime);
    const speedMultiplier = dto.speedMultiplier || 1;

    // Generate a unique channel name for WebSocket replay
    const channelId = `track-replay-${entityId}-${Date.now()}`;

    return {
      channelId,
      entityId,
      startTime,
      endTime,
      speedMultiplier,
      wsUrl: `/ws/track-replay/${channelId}`,
      message: `Subscribe to WebSocket channel '${channelId}' to receive replay events`,
    };
  }

  /**
   * Kafka consumer: events.entity.position
   * Records every position update as a track point.
   */
  @MessagePattern('events.entity.position')
  async handlePositionEvent(
    @Payload()
    payload: {
      entityId: string;
      latitude: number;
      longitude: number;
      heading?: number;
      speedKnots?: number;
      course?: number;
      source?: string;
      timestamp: string;
      altitude?: number;
      velocityNorth?: number;
      velocityEast?: number;
      velocityUp?: number;
      circularError?: number;
      feedId?: string;
      trackProcessingState?: string;
      accelNorth?: number;
      accelEast?: number;
      accelUp?: number;
      posCovariance?: number[];
      posVelCovariance?: number[];
      velCovariance?: number[];
      altitudeError?: number;
      sensorId?: string;
    },
  ) {
    await this.trackService.handlePositionEvent(payload);
  }
}
