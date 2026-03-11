import { Injectable, Logger } from '@nestjs/common';
import type { AuthenticatedUser } from './jwt.strategy';

export interface AuthAuditEvent {
  eventType: 'LOGIN' | 'LOGOUT' | 'ACCESS_DENIED' | 'TOKEN_REFRESH' | 'ROLE_CHECK_FAILED' | 'CLASSIFICATION_CHECK_FAILED';
  userId?: string;
  username?: string;
  ip?: string;
  userAgent?: string;
  resource?: string;
  method?: string;
  requiredRoles?: string[];
  userRoles?: string[];
  requiredClassification?: string;
  userClassification?: string;
  timestamp: string;
}

@Injectable()
export class AuthAuditService {
  private readonly logger = new Logger('AuthAudit');

  logAccess(user: AuthenticatedUser, method: string, resource: string, ip?: string): void {
    this.logger.log(
      JSON.stringify({
        eventType: 'ACCESS',
        userId: user.userId,
        username: user.username,
        method,
        resource,
        ip,
        timestamp: new Date().toISOString(),
      }),
    );
  }

  logAccessDenied(
    user: AuthenticatedUser | undefined,
    method: string,
    resource: string,
    reason: string,
    details?: Partial<AuthAuditEvent>,
  ): void {
    this.logger.warn(
      JSON.stringify({
        eventType: 'ACCESS_DENIED',
        userId: user?.userId,
        username: user?.username,
        method,
        resource,
        reason,
        ...details,
        timestamp: new Date().toISOString(),
      }),
    );
  }

  logRoleCheckFailed(
    user: AuthenticatedUser,
    resource: string,
    requiredRoles: string[],
  ): void {
    this.logger.warn(
      JSON.stringify({
        eventType: 'ROLE_CHECK_FAILED',
        userId: user.userId,
        username: user.username,
        resource,
        requiredRoles,
        userRoles: user.roles,
        timestamp: new Date().toISOString(),
      }),
    );
  }

  logClassificationCheckFailed(
    user: AuthenticatedUser,
    resource: string,
    requiredClassification: string,
  ): void {
    this.logger.warn(
      JSON.stringify({
        eventType: 'CLASSIFICATION_CHECK_FAILED',
        userId: user.userId,
        username: user.username,
        resource,
        requiredClassification,
        userClassification: user.classificationLevel,
        timestamp: new Date().toISOString(),
      }),
    );
  }
}
