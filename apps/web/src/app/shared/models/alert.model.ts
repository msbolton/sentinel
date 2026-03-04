import { AlertSeverity, AlertType } from '@sentinel/proto-gen';
export { AlertSeverity, AlertType };

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
