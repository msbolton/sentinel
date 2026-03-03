import { BoundingBox, Coordinate, PaginationRequest, PaginationResponse } from './common';
import { EntityType } from './entity';

// Canonical definitions in @sentinel/proto-gen (proto/alert.proto)
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
  GEOFENCE_ENTRY = 'GEOFENCE_ENTRY',
  GEOFENCE_EXIT = 'GEOFENCE_EXIT',
  PATTERN_MATCH = 'PATTERN_MATCH',
  CUSTOM = 'CUSTOM',
  PROXIMITY_ALERT = 'PROXIMITY_ALERT',
  ENTITY_LOST = 'ENTITY_LOST',
  PATTERN_DETECTED = 'PATTERN_DETECTED',
  LINK_CHANGE = 'LINK_CHANGE',
  CLASSIFICATION_CHANGE = 'CLASSIFICATION_CHANGE',
  SYSTEM = 'SYSTEM',
}

export enum RuleType {
  GEOFENCE = 'GEOFENCE',
  SPEED_THRESHOLD = 'SPEED_THRESHOLD',
  PROXIMITY = 'PROXIMITY',
  PATTERN = 'PATTERN',
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
