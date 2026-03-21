import { Injectable, Logger, NotFoundException } from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { TrackPoint } from './track-point.entity';
import { TrackBatchService } from './track-batch.service';

export interface TrackSegment {
  startTime: Date;
  endTime: Date;
  points: TrackPointResult[];
}

export interface TrackPointResult {
  id: string;
  entityId: string;
  latitude: number;
  longitude: number;
  heading: number | null;
  speedKnots: number | null;
  course: number | null;
  altitude: number | null;
  source: string | null;
  timestamp: Date;
}

@Injectable()
export class TrackService {
  private readonly logger = new Logger(TrackService.name);
  private static readonly SEGMENT_GAP_MS = 30 * 60 * 1000; // 30 minutes

  constructor(
    @InjectRepository(TrackPoint)
    private readonly trackPointRepo: Repository<TrackPoint>,
    private readonly batchService: TrackBatchService,
  ) {}

  /**
   * Record a new track point. Uses the batch service for optimized bulk inserts.
   */
  async recordPoint(
    entityId: string,
    lat: number,
    lng: number,
    heading: number | null,
    speed: number | null,
    course: number | null,
    source: string | null,
    timestamp: Date,
    altitude: number | null = null,
    velocityNorth: number | null = null,
    velocityEast: number | null = null,
    velocityUp: number | null = null,
    circularError: number | null = null,
    feedId: string | null = null,
    trackProcessingState: string | null = null,
    accelNorth: number | null = null,
    accelEast: number | null = null,
    accelUp: number | null = null,
    posCovariance: number[] | null = null,
    posVelCovariance: number[] | null = null,
    velCovariance: number[] | null = null,
    altitudeError: number | null = null,
    sensorId: string | null = null,
  ): Promise<void> {
    await this.batchService.addPoint({
      entityId,
      latitude: lat,
      longitude: lng,
      heading,
      speedKnots: speed,
      course,
      source,
      timestamp,
      altitude,
      velocityNorth,
      velocityEast,
      velocityUp,
      circularError,
      feedId,
      trackProcessingState,
      accelNorth,
      accelEast,
      accelUp,
      posCovariance,
      posVelCovariance,
      velCovariance,
      altitudeError,
      sensorId,
    });
  }

  /**
   * Query track history with optional Douglas-Peucker simplification via PostGIS ST_Simplify.
   */
  async getHistory(
    entityId: string,
    startTime?: Date,
    endTime?: Date,
    maxPoints?: number,
    simplify?: number,
  ): Promise<TrackPointResult[]> {
    let query = this.trackPointRepo
      .createQueryBuilder('tp')
      .select([
        'tp.id AS id',
        'tp."entityId" AS "entityId"',
        'ST_Y(tp.position) AS latitude',
        'ST_X(tp.position) AS longitude',
        'tp.heading AS heading',
        'tp."speedKnots" AS "speedKnots"',
        'tp.course AS course',
        'tp.altitude AS altitude',
        'tp.source AS source',
        'tp.timestamp AS timestamp',
      ])
      .where('tp."entityId" = :entityId', { entityId })
      .orderBy('tp.timestamp', 'ASC');

    if (startTime) {
      query = query.andWhere('tp.timestamp >= :startTime', { startTime });
    }
    if (endTime) {
      query = query.andWhere('tp.timestamp <= :endTime', { endTime });
    }
    if (maxPoints) {
      query = query.limit(maxPoints);
    }

    const results = await query.getRawMany();

    if (simplify && simplify > 0 && results.length > 0) {
      return this.getSimplifiedTrack(entityId, startTime, endTime, simplify, maxPoints);
    }

    return results;
  }

  /**
   * Get simplified track using PostGIS ST_Simplify with Douglas-Peucker algorithm.
   */
  private async getSimplifiedTrack(
    entityId: string,
    startTime?: Date,
    endTime?: Date,
    tolerance?: number,
    maxPoints?: number,
  ): Promise<TrackPointResult[]> {
    const params: (string | Date | number)[] = [entityId];
    let paramIdx = 2;

    let timeFilter = '';
    if (startTime) {
      timeFilter += ` AND tp.timestamp >= $${paramIdx}`;
      params.push(startTime);
      paramIdx++;
    }
    if (endTime) {
      timeFilter += ` AND tp.timestamp <= $${paramIdx}`;
      params.push(endTime);
      paramIdx++;
    }

    const toleranceValue = tolerance || 0.0001;
    params.push(toleranceValue);
    const toleranceIdx = paramIdx;
    paramIdx++;

    let limitClause = '';
    if (maxPoints) {
      limitClause = ` LIMIT $${paramIdx}`;
      params.push(maxPoints);
    }

    const sql = `
      WITH track_line AS (
        SELECT
          ST_Simplify(
            ST_MakeLine(tp.position ORDER BY tp.timestamp),
            $${toleranceIdx}
          ) AS simplified_line
        FROM sentinel.track_points tp
        WHERE tp."entityId" = $1 ${timeFilter}
      ),
      simplified_points AS (
        SELECT (ST_DumpPoints(simplified_line)).geom AS geom
        FROM track_line
      )
      SELECT
        tp.id,
        tp."entityId",
        ST_Y(tp.position) AS latitude,
        ST_X(tp.position) AS longitude,
        tp.heading,
        tp."speedKnots",
        tp.course,
        tp.altitude,
        tp.source,
        tp.timestamp
      FROM sentinel.track_points tp
      WHERE tp."entityId" = $1 ${timeFilter}
      ORDER BY tp.timestamp ASC
      ${limitClause}
    `;

    return this.trackPointRepo.query(sql, params);
  }

  /**
   * Return track segments where gaps > 30 minutes create new segments.
   */
  async getSegments(
    entityId: string,
    startTime?: Date,
    endTime?: Date,
  ): Promise<TrackSegment[]> {
    const points = await this.getHistory(entityId, startTime, endTime);

    if (points.length === 0) {
      return [];
    }

    const segments: TrackSegment[] = [];
    let currentSegment: TrackPointResult[] = [points[0]];

    for (let i = 1; i < points.length; i++) {
      const prevTime = new Date(points[i - 1].timestamp).getTime();
      const currTime = new Date(points[i].timestamp).getTime();
      const gap = currTime - prevTime;

      if (gap > TrackService.SEGMENT_GAP_MS) {
        // Gap exceeds 30 min - close current segment and start new one
        segments.push({
          startTime: new Date(currentSegment[0].timestamp),
          endTime: new Date(currentSegment[currentSegment.length - 1].timestamp),
          points: currentSegment,
        });
        currentSegment = [points[i]];
      } else {
        currentSegment.push(points[i]);
      }
    }

    // Push last segment
    segments.push({
      startTime: new Date(currentSegment[0].timestamp),
      endTime: new Date(currentSegment[currentSegment.length - 1].timestamp),
      points: currentSegment,
    });

    return segments;
  }

  /**
   * Get most recent track point for each of the given entity IDs.
   */
  async getLatestPositions(entityIds: string[]): Promise<TrackPointResult[]> {
    if (entityIds.length === 0) {
      return [];
    }

    const sql = `
      SELECT DISTINCT ON (tp."entityId")
        tp.id,
        tp."entityId",
        ST_Y(tp.position) AS latitude,
        ST_X(tp.position) AS longitude,
        tp.heading,
        tp."speedKnots",
        tp.course,
        tp.altitude,
        tp.source,
        tp.timestamp
      FROM sentinel.track_points tp
      WHERE tp."entityId" = ANY($1)
      ORDER BY tp."entityId", tp.timestamp DESC
    `;

    return this.trackPointRepo.query(sql, [entityIds]);
  }

  /**
   * Handle incoming position events from Kafka.
   * Records every position update as a track point.
   */
  async handlePositionEvent(payload: {
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
  }): Promise<void> {
    await this.batchService.addPoint({
      entityId: payload.entityId,
      latitude: payload.latitude,
      longitude: payload.longitude,
      heading: payload.heading ?? null,
      speedKnots: payload.speedKnots ?? null,
      course: payload.course ?? null,
      source: payload.source ?? null,
      timestamp: new Date(payload.timestamp),
      altitude: payload.altitude ?? null,
      velocityNorth: payload.velocityNorth ?? null,
      velocityEast: payload.velocityEast ?? null,
      velocityUp: payload.velocityUp ?? null,
      circularError: payload.circularError ?? null,
      feedId: payload.feedId ?? null,
      trackProcessingState: payload.trackProcessingState ?? null,
      accelNorth: payload.accelNorth ?? null,
      accelEast: payload.accelEast ?? null,
      accelUp: payload.accelUp ?? null,
      posCovariance: payload.posCovariance ?? null,
      posVelCovariance: payload.posVelCovariance ?? null,
      velCovariance: payload.velCovariance ?? null,
      altitudeError: payload.altitudeError ?? null,
      sensorId: payload.sensorId ?? null,
    });

    this.logger.debug(
      `Recorded track point for entity ${payload.entityId}`,
    );
  }
}
