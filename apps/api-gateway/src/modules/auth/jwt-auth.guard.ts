import {
  Injectable,
  ExecutionContext,
  ForbiddenException,
  Logger,
} from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import { AuthGuard } from '@nestjs/passport';
import { Observable } from 'rxjs';
import { ROLES_KEY } from './decorators/roles.decorator';
import {
  CLASSIFICATION_KEY,
  ClassificationLevel,
  CLASSIFICATION_HIERARCHY,
} from './decorators/classification.decorator';
import type { AuthenticatedUser } from './jwt.strategy';

@Injectable()
export class JwtAuthGuard extends AuthGuard('jwt') {
  private readonly logger = new Logger(JwtAuthGuard.name);

  constructor(private readonly reflector: Reflector) {
    super();
  }

  override canActivate(
    context: ExecutionContext,
  ): boolean | Promise<boolean> | Observable<boolean> {
    // First, run the parent JWT validation
    const canActivate = super.canActivate(context);

    if (canActivate instanceof Observable) {
      // For Observable, we cannot chain synchronous logic directly.
      // Convert to promise for consistency.
      return new Promise<boolean>((resolve, reject) => {
        (canActivate as Observable<boolean>).subscribe({
          next: (result) => {
            if (!result) {
              resolve(false);
              return;
            }
            try {
              this.validateRolesAndClassification(context);
              resolve(true);
            } catch (err) {
              reject(err);
            }
          },
          error: (err) => reject(err),
        });
      });
    }

    if (canActivate instanceof Promise) {
      return canActivate.then((result) => {
        if (!result) return false;
        this.validateRolesAndClassification(context);
        return true;
      });
    }

    if (!canActivate) return false;
    this.validateRolesAndClassification(context);
    return true;
  }

  /**
   * After JWT validation succeeds, check role-based and classification-based
   * access requirements declared via decorators on the handler/controller.
   */
  private validateRolesAndClassification(context: ExecutionContext): void {
    const request = context.switchToHttp().getRequest();
    const user = request.user as AuthenticatedUser | undefined;

    if (!user) {
      throw new ForbiddenException('No authenticated user found');
    }

    // Check required roles
    const requiredRoles = this.reflector.getAllAndOverride<string[] | undefined>(
      ROLES_KEY,
      [context.getHandler(), context.getClass()],
    );

    if (requiredRoles && requiredRoles.length > 0) {
      const hasRole = requiredRoles.some((role) => user.roles.includes(role));
      if (!hasRole) {
        this.logger.warn(
          `User ${user.username} denied: requires one of [${requiredRoles.join(', ')}], has [${user.roles.join(', ')}]`,
        );
        throw new ForbiddenException(
          'Insufficient role permissions for this resource',
        );
      }
    }

    // Check classification level
    const requiredClassification =
      this.reflector.getAllAndOverride<ClassificationLevel | undefined>(
        CLASSIFICATION_KEY,
        [context.getHandler(), context.getClass()],
      );

    if (requiredClassification) {
      const userLevel = (user.classificationLevel?.toUpperCase() ??
        'UNCLASSIFIED') as ClassificationLevel;
      const userRank = CLASSIFICATION_HIERARCHY[userLevel] ?? 0;
      const requiredRank = CLASSIFICATION_HIERARCHY[requiredClassification] ?? 0;

      if (userRank < requiredRank) {
        this.logger.warn(
          `User ${user.username} denied: requires classification ${requiredClassification}, has ${userLevel}`,
        );
        throw new ForbiddenException(
          `Insufficient classification clearance. Required: ${requiredClassification}`,
        );
      }
    }
  }
}
