import {
  Controller,
  Get,
  Param,
  Query,
  Req,
  Res,
  UseGuards,
  ParseUUIDPipe,
  Logger,
} from '@nestjs/common';
import { Request, Response } from 'express';
import { HttpService } from '@nestjs/axios';
import { firstValueFrom } from 'rxjs';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { TracksService } from './tracks.service';

@Controller('tracks')
@UseGuards(JwtAuthGuard)
export class TracksController {
  private readonly logger = new Logger(TracksController.name);

  constructor(
    private readonly tracksService: TracksService,
    private readonly httpService: HttpService,
  ) {}

  @Get(':entityId')
  async getHistory(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: Record<string, any>,
  ) {
    return this.tracksService.getHistory(entityId, query);
  }

  @Get(':entityId/latest')
  async getLatestPosition(
    @Param('entityId', ParseUUIDPipe) entityId: string,
  ) {
    return this.tracksService.getLatestPosition(entityId);
  }

  @Get(':entityId/segments')
  async getSegments(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: Record<string, any>,
  ) {
    return this.tracksService.getSegments(entityId, query);
  }

  /**
   * SSE replay passthrough — pipes the upstream SSE stream directly to the client.
   * Auth is handled by JwtAuthGuard on this controller.
   * For EventSource clients, JWT token is passed via ?token= query param.
   */
  @Get(':entityId/replay-stream')
  async replayStream(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: Record<string, any>,
    @Req() req: Request,
    @Res() res: Response,
  ) {
    const url = this.tracksService.getReplayStreamUrl(entityId);

    // Strip token from query before forwarding
    const { token, ...forwardQuery } = query;

    try {
      const upstream = await firstValueFrom(
        this.httpService.get(url, {
          params: forwardQuery,
          responseType: 'stream',
          headers: { Accept: 'text/event-stream' },
        }),
      );

      res.setHeader('Content-Type', 'text/event-stream');
      res.setHeader('Cache-Control', 'no-cache');
      res.setHeader('Connection', 'keep-alive');
      res.setHeader('X-Accel-Buffering', 'no');

      // Close upstream when client disconnects to prevent dangling connections
      req.on('close', () => upstream.data.destroy());

      upstream.data.pipe(res);
    } catch (err) {
      this.logger.warn(`SSE proxy error: ${err instanceof Error ? err.message : String(err)}`);
      res.status(503).json({ message: 'Track service unavailable' });
    }
  }
}
