export interface Alert {
  id: string;
  title: string;
  message: string;
  severity: AlertSeverity;
  alertType: AlertType;
  entityId?: string;
  entityName?: string;
  ruleId?: string;
  position?: {
    latitude: number;
    longitude: number;
  };
  acknowledged: boolean;
  acknowledgedBy?: string;
  acknowledgedAt?: string;
  metadata: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}

export enum AlertSeverity {
  CRITICAL = 'CRITICAL',
  HIGH = 'HIGH',
  MEDIUM = 'MEDIUM',
  LOW = 'LOW',
  INFO = 'INFO',
}

export enum AlertType {
  GEOFENCE_BREACH = 'GEOFENCE_BREACH',
  PROXIMITY_ALERT = 'PROXIMITY_ALERT',
  SPEED_ANOMALY = 'SPEED_ANOMALY',
  NEW_ENTITY = 'NEW_ENTITY',
  ENTITY_LOST = 'ENTITY_LOST',
  PATTERN_DETECTED = 'PATTERN_DETECTED',
  LINK_CHANGE = 'LINK_CHANGE',
  CLASSIFICATION_CHANGE = 'CLASSIFICATION_CHANGE',
  SYSTEM = 'SYSTEM',
}

export interface AlertRule {
  id: string;
  name: string;
  description: string;
  alertType: AlertType;
  severity: AlertSeverity;
  conditions: Record<string, unknown>;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface AlertQuery {
  severity?: AlertSeverity;
  alertType?: AlertType;
  acknowledged?: boolean;
  entityId?: string;
  limit?: number;
  offset?: number;
}
