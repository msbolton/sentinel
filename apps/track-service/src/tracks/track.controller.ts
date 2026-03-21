import {
  Controller,
  Get,
  MessageEvent,
  Param,
  Query,
  ParseUUIDPipe,
  Sse,
} from '@nestjs/common';
import { MessagePattern, Payload } from '@nestjs/microservices';
import { Observable } from 'rxjs';
import { TrackService } from './track.service';
import { QueryTrackDto, ReplayStreamDto } from './dto/query-track.dto';

@Controller('tracks')
export class TrackController {
  constructor(private readonly trackService: TrackService) {}

  /**
   * GET /tracks/:entityId/replay-stream
   * Stream track points via SSE at an adjustable speed multiplier.
   */
  @Sse(':entityId/replay-stream')
  replayStream(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: ReplayStreamDto,
  ): Observable<MessageEvent> {
    const startTime = new Date(query.startTime);
    const endTime = new Date(query.endTime);
    const speedMultiplier = query.speedMultiplier || 1;

    return this.trackService.replayStream(
      entityId,
      startTime,
      endTime,
      speedMultiplier,
    );
  }

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
