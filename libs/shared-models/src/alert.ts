import { BoundingBox, Coordinate, PaginationRequest, PaginationResponse } from './common';
import { EntityType } from './entity';

export enum AlertSeverity {
  INFO = 'INFO',
  LOW = 'LOW',
  MEDIUM = 'MEDIUM',
  HIGH = 'HIGH',
  CRITICAL = 'CRITICAL',
}

export enum AlertType {
  GEOFENCE_BREACH = 'GEOFENCE_BREACH',
  SPEED_ANOMALY = 'SPEED_ANOMALY',
  PATTERN_DEVIATION = 'PATTERN_DEVIATION',
  PROXIMITY = 'PROXIMITY',
  COMMUNICATION_ANOMALY = 'COMMUNICATION_ANOMALY',
  NEW_ENTITY = 'NEW_ENTITY',
  ENTITY_CLASSIFICATION_CHANGE = 'ENTITY_CLASSIFICATION_CHANGE',
}

export interface GeofenceRule {
  ruleId: string;
  name: string;
  polygon: Coordinate[];
  monitoredEntityTypes: EntityType[];
  triggerOnEnter: boolean;
  triggerOnExit: boolean;
}

export interface Alert {
  alertId: string;
  alertType: AlertType;
  severity: AlertSeverity;
  title: string;
  description: string;
  entityId: string;
  relatedEntityIds: string[];
  position?: Coordinate;
  ruleId?: string;
  metadata: Record<string, string>;
  createdAt: string;
  acknowledgedAt?: string;
  acknowledgedBy?: string;
  resolvedAt?: string;
}

export interface AlertRule {
  id: string;
  name: string;
  ruleType: AlertType;
  config: Record<string, unknown>;
  monitoredEntityTypes: EntityType[];
  severity: AlertSeverity;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export enum AlertEventType {
  TRIGGERED = 'TRIGGERED',
  ACKNOWLEDGED = 'ACKNOWLEDGED',
  RESOLVED = 'RESOLVED',
}

export interface AlertEvent {
  alert: Alert;
  eventType: AlertEventType;
}

export interface GetAlertsRequest extends PaginationRequest {
  severityMin?: AlertSeverity;
  alertTypes?: AlertType[];
  entityId?: string;
  boundingBox?: BoundingBox;
  startTime?: string;
  endTime?: string;
  acknowledged?: boolean;
}

export interface GetAlertsResponse {
  alerts: Alert[];
  pagination: PaginationResponse;
}
