import {
  Module,
  Injectable,
  CanActivate,
  ExecutionContext,
  Logger,
  SetMetadata,
} from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import {
  AUTH_HEADER,
  USER_ID_HEADER,
  USER_ROLES_HEADER,
  USER_CLASSIFICATION_HEADER,
} from './auth.constants';

/**
 * Represents a user context extracted from service-to-service headers.
 * When the API Gateway forwards a request, it attaches these headers
 * so downstream services know who the original caller is.
 */
export interface ServiceUser {
  userId: string;
  roles: string[];
  classificationLevel: string;
}

export const SERVICE_ROLES_KEY = 'sentinel:service-roles';

/**
 * Decorator to require specific roles on a downstream service endpoint.
 */
export const ServiceRoles = (...roles: string[]) =>
  SetMetadata(SERVICE_ROLES_KEY, roles);

/**
 * Guard for downstream NestJS services that extracts user context
 * from headers set by the API Gateway.
 *
 * In development mode (NODE_ENV !== 'production'), it injects a dev user
 * to match the API Gateway's dev-mode behavior.
 */
@Injectable()
export class ServiceAuthGuard implements CanActivate {
  private readonly logger = new Logger(ServiceAuthGuard.name);
  private readonly isProduction = process.env['NODE_ENV'] === 'production';

  constructor(private readonly reflector: Reflector) {}

  canActivate(context: ExecutionContext): boolean {
    const request = context.switchToHttp().getRequest();

    if (!this.isProduction) {
      // Dev mode: inject dev user matching API Gateway
      request.serviceUser = {
        userId: '00000000-0000-0000-0000-000000000001',
        roles: ['sentinel-analyst', 'sentinel-operator', 'sentinel-admin'],
        classificationLevel: 'TOP_SECRET',
      } satisfies ServiceUser;
      return true;
    }

    // Extract user context from headers set by API Gateway
    const userId = request.headers[USER_ID_HEADER];
    const rolesHeader = request.headers[USER_ROLES_HEADER];
    const classification = request.headers[USER_CLASSIFICATION_HEADER] ?? 'UNCLASSIFIED';

    if (!userId) {
      this.logger.warn('Missing user ID header - request not authenticated');
      return false;
    }

    const roles = rolesHeader ? rolesHeader.split(',') : [];

    request.serviceUser = {
      userId,
      roles,
      classificationLevel: classification,
    } satisfies ServiceUser;

    // Check required roles if specified
    const requiredRoles = this.reflector.getAllAndOverride<string[] | undefined>(
      SERVICE_ROLES_KEY,
      [context.getHandler(), context.getClass()],
    );

    if (requiredRoles && requiredRoles.length > 0) {
      const hasRole = requiredRoles.some((role) => roles.includes(role));
      if (!hasRole) {
        this.logger.warn(
          `User ${userId} denied: requires [${requiredRoles.join(', ')}], has [${roles.join(', ')}]`,
        );
        return false;
      }
    }

    return true;
  }
}

@Module({
  providers: [ServiceAuthGuard],
  exports: [ServiceAuthGuard],
})
export class ServiceAuthModule {}
