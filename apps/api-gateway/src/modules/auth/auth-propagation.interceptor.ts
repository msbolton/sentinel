import {
  Injectable,
  NestInterceptor,
  ExecutionContext,
  CallHandler,
} from '@nestjs/common';
import { Observable } from 'rxjs';
import {
  USER_ID_HEADER,
  USER_ROLES_HEADER,
  USER_CLASSIFICATION_HEADER,
} from '@sentinel/common';
import type { AuthenticatedUser } from './jwt.strategy';

/**
 * Interceptor that extracts the authenticated user from the request
 * and stores it in a request-scoped context so that downstream HTTP calls
 * (from the API Gateway to microservices) can propagate the user's identity.
 *
 * Services making HTTP calls to downstream services should use the
 * `getAuthHeaders()` helper to attach these headers.
 */
@Injectable()
export class AuthPropagationInterceptor implements NestInterceptor {
  intercept(context: ExecutionContext, next: CallHandler): Observable<any> {
    const request = context.switchToHttp().getRequest();
    const user = request.user as AuthenticatedUser | undefined;

    if (user) {
      // Store auth headers on request for downstream propagation
      request.authHeaders = {
        [USER_ID_HEADER]: user.userId,
        [USER_ROLES_HEADER]: user.roles.join(','),
        [USER_CLASSIFICATION_HEADER]: user.classificationLevel,
      };
    }

    return next.handle();
  }
}

/**
 * Helper to extract auth headers from a request object for forwarding
 * to downstream services.
 */
export function getAuthHeaders(request: any): Record<string, string> {
  return request?.authHeaders ?? {};
}
