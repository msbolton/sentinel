import { Injectable, Logger } from '@nestjs/common';
import { MessagePattern, Payload } from '@nestjs/microservices';
import { AlertService } from './alert.service';

interface PositionEventPayload {
  entityId: string;
  entityType: string;
  latitude: number;
  longitude: number;
  heading?: number;
  speedKnots?: number;
  course?: number;
  source?: string;
  timestamp: string;
}

/**
 * Kafka consumer that evaluates incoming entity position events
 * against all active alert rules (geofence and speed anomaly).
 */
@Injectable()
export class AlertEvaluatorService {
  private readonly logger = new Logger(AlertEvaluatorService.name);

  constructor(private readonly alertService: AlertService) {}

  /**
   * Subscribe to events.entity.position from Kafka.
   * For each position update:
   * - Evaluate geofence rules
   * - Evaluate speed anomaly rules
   * - Fire alerts to Kafka 'alerts.geofence' or 'alerts.anomaly'
   */
  @MessagePattern('events.entity.position')
  async handlePositionEvent(
    @Payload() payload: PositionEventPayload,
  ): Promise<void> {
    const {
      entityId,
      entityType,
      latitude,
      longitude,
      speedKnots,
    } = payload;

    this.logger.debug(
      `Evaluating position event for entity ${entityId} at [${latitude}, ${longitude}]`,
    );

    // Evaluate geofence rules
    try {
      const geofenceAlerts =
        await this.alertService.evaluateEntityPosition(
          entityId,
          latitude,
          longitude,
          entityType,
        );

      if (geofenceAlerts.length > 0) {
        this.logger.log(
          `Generated ${geofenceAlerts.length} geofence alert(s) for entity ${entityId}`,
        );
      }
    } catch (error) {
      this.logger.error(
        `Geofence evaluation failed for entity ${entityId}`,
        error instanceof Error ? error.stack : String(error),
      );
    }

    // Evaluate speed anomaly rules
    if (speedKnots !== undefined && speedKnots !== null) {
      try {
        const speedAlerts =
          await this.alertService.evaluateSpeedAnomaly(
            entityId,
            speedKnots,
            entityType,
          );

        if (speedAlerts.length > 0) {
          this.logger.log(
            `Generated ${speedAlerts.length} speed anomaly alert(s) for entity ${entityId}`,
          );
        }
      } catch (error) {
        this.logger.error(
          `Speed anomaly evaluation failed for entity ${entityId}`,
          error instanceof Error ? error.stack : String(error),
        );
      }
    }
  }
}
