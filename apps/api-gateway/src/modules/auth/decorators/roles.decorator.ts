import { SetMetadata } from '@nestjs/common';

/**
 * Metadata key used to store required roles on route handlers or controllers.
 */
export const ROLES_KEY = 'sentinel:roles';

/**
 * Decorator that specifies which roles are allowed to access a route.
 * The user must have at least one of the specified roles.
 *
 * @example
 * ```typescript
 * @Roles('admin', 'analyst')
 * @Get('classified-data')
 * getClassifiedData() { ... }
 * ```
 */
export const Roles = (...roles: string[]) => SetMetadata(ROLES_KEY, roles);
