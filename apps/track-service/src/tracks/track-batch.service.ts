import { Injectable, Logger, OnModuleDestroy } from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { TrackPoint } from './track-point.entity';

interface BufferedPoint {
  entityId: string;
  latitude: number;
  longitude: number;
  heading: number | null;
  speedKnots: number | null;
  course: number | null;
  source: string | null;
  timestamp: Date;
  altitude: number | null;
  velocityNorth: number | null;
  velocityEast: number | null;
  velocityUp: number | null;
  circularError: number | null;
}

/**
 * Batching service that buffers track points and flushes them
 * in bulk INSERT batches for optimal TimescaleDB write performance.
 *
 * Flushes when either:
 * - Buffer reaches 100 points, OR
 * - 1 second has elapsed since last flush
 * whichever comes first.
 */
@Injectable()
export class TrackBatchService implements OnModuleDestroy {
  private readonly logger = new Logger(TrackBatchService.name);
  private buffer: BufferedPoint[] = [];
  private flushTimer: NodeJS.Timeout | null = null;
  private readonly BATCH_SIZE = 100;
  private readonly FLUSH_INTERVAL_MS = 1000;
  private readonly MAX_BUFFER_SIZE = 10_000;
  private isFlushing = false;

  constructor(
    @InjectRepository(TrackPoint)
    private readonly trackPointRepo: Repository<TrackPoint>,
  ) {
    this.startFlushTimer();
  }

  onModuleDestroy() {
    if (this.flushTimer) {
      clearInterval(this.flushTimer);
      this.flushTimer = null;
    }
    // Final flush on shutdown
    this.flush().catch((err) =>
      this.logger.error('Error during final flush', err),
    );
  }

  /**
   * Add a track point to the buffer. Will trigger a flush if buffer is full.
   */
  async addPoint(point: BufferedPoint): Promise<void> {
    if (this.buffer.length >= this.MAX_BUFFER_SIZE) {
      this.logger.warn(
        `Buffer full (${this.MAX_BUFFER_SIZE} points), dropping incoming point`,
      );
      return;
    }

    this.buffer.push(point);

    if (this.buffer.length >= this.BATCH_SIZE) {
      await this.flush();
    }
  }

  /**
   * Flush all buffered points to TimescaleDB using a bulk INSERT.
   */
  async flush(): Promise<void> {
    if (this.isFlushing || this.buffer.length === 0) {
      return;
    }

    this.isFlushing = true;
    const pointsToFlush = [...this.buffer];
    this.buffer = [];

    try {
      const values = pointsToFlush.map((p) => ({
        entityId: p.entityId,
        position: () =>
          `ST_SetSRID(ST_MakePoint(${p.longitude}, ${p.latitude}), 4326)`,
        heading: p.heading,
        speedKnots: p.speedKnots,
        course: p.course,
        source: p.source,
        timestamp: p.timestamp,
      }));

      // Use query builder for bulk insert with raw PostGIS geometry
      const queryBuilder = this.trackPointRepo
        .createQueryBuilder()
        .insert()
        .into(TrackPoint);

      // Build raw SQL for bulk insert with ST_MakePoint
      const paramIndex = { current: 1 };
      const valuesClauses: string[] = [];
      const params: (string | number | Date | null)[] = [];

      for (const point of pointsToFlush) {
        const idx = paramIndex.current;
        valuesClauses.push(
          `($${idx}, $${idx + 1}, ST_SetSRID(ST_MakePoint($${idx + 2}, $${idx + 3}), 4326), $${idx + 4}, $${idx + 5}, $${idx + 6}, $${idx + 7}, $${idx + 8}, $${idx + 9}, $${idx + 10}, $${idx + 11}, $${idx + 12})`,
        );
        params.push(
          point.entityId,
          point.source,
          point.longitude,
          point.latitude,
          point.heading,
          point.speedKnots,
          point.course,
          point.timestamp,
          point.altitude,
          point.velocityNorth,
          point.velocityEast,
          point.velocityUp,
          point.circularError,
        );
        paramIndex.current += 13;
      }

      const sql = `
        INSERT INTO sentinel.track_points
          ("entityId", "source", "position", "heading", "speedKnots", "course", "timestamp", "altitude", "velocityNorth", "velocityEast", "velocityUp", "circularError")
        VALUES ${valuesClauses.join(', ')}
      `;

      await this.trackPointRepo.query(sql, params);

      this.logger.debug(`Flushed ${pointsToFlush.length} track points`);
    } catch (error) {
      this.logger.error(
        `Failed to flush ${pointsToFlush.length} track points`,
        error instanceof Error ? error.stack : String(error),
      );
      // Re-add failed points to buffer for retry, respecting max size
      const combinedSize = this.buffer.length + pointsToFlush.length;
      if (combinedSize > this.MAX_BUFFER_SIZE) {
        const dropped = combinedSize - this.MAX_BUFFER_SIZE;
        this.logger.error(
          `Buffer would exceed max size on retry; dropping ${dropped} oldest failed points`,
        );
        const keepCount = this.MAX_BUFFER_SIZE - this.buffer.length;
        this.buffer.unshift(...pointsToFlush.slice(pointsToFlush.length - keepCount));
      } else {
        this.buffer.unshift(...pointsToFlush);
      }
    } finally {
      this.isFlushing = false;
    }
  }

  /**
   * Start the periodic flush timer.
   */
  private startFlushTimer(): void {
    this.flushTimer = setInterval(() => {
      this.flush().catch((err) =>
        this.logger.error('Periodic flush error', err),
      );
    }, this.FLUSH_INTERVAL_MS);
  }

  /**
   * Return current buffer size (for monitoring).
   */
  get bufferSize(): number {
    return this.buffer.length;
  }
}
