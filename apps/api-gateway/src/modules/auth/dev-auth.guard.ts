import { Injectable, ExecutionContext, Logger } from '@nestjs/common';
import type { AuthenticatedUser } from './jwt.strategy';

/**
 * Development-only auth guard that bypasses JWT validation and injects
 * a synthetic user into the request. Used when NODE_ENV !== 'production'
 * to allow API access without a running Keycloak instance.
 */
@Injectable()
export class DevAuthGuard {
  private readonly logger = new Logger(DevAuthGuard.name);

  private readonly devUser: AuthenticatedUser = {
    userId: '00000000-0000-0000-0000-000000000001',
    username: 'dev-operator',
    email: 'operator@sentinel.local',
    name: 'Dev Operator',
    roles: ['analyst', 'operator', 'admin'],
    realmRoles: ['analyst', 'operator', 'admin'],
    clientRoles: [],
    classificationLevel: 'TOP_SECRET',
  };

  canActivate(context: ExecutionContext): boolean {
    const request = context.switchToHttp().getRequest();
    request.user = this.devUser;

    this.logger.debug(
      `Dev auth bypass: injected user '${this.devUser.username}' with roles [${this.devUser.roles.join(', ')}]`,
    );

    return true;
  }
}
