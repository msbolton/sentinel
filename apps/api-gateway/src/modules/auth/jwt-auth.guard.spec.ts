import { ExecutionContext, ForbiddenException } from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import { JwtAuthGuard } from './jwt-auth.guard';
import { ROLES_KEY } from './decorators/roles.decorator';
import { CLASSIFICATION_KEY } from './decorators/classification.decorator';

// Save and restore NODE_ENV
const originalNodeEnv = process.env['NODE_ENV'];

function createMockContext(user?: Record<string, unknown>): ExecutionContext {
  const request = { user } as Record<string, unknown>;
  return {
    switchToHttp: () => ({
      getRequest: () => request,
      getResponse: () => ({}),
    }),
    getHandler: () => jest.fn(),
    getClass: () => jest.fn(),
    getType: () => 'http' as const,
    getArgs: () => [],
    getArgByIndex: () => null,
    switchToRpc: () => ({} as never),
    switchToWs: () => ({} as never),
  } as unknown as ExecutionContext;
}

describe('JwtAuthGuard', () => {
  let guard: JwtAuthGuard;
  let reflector: Reflector;

  beforeEach(() => {
    reflector = new Reflector();
    guard = new JwtAuthGuard(reflector);
  });

  afterEach(() => {
    process.env['NODE_ENV'] = originalNodeEnv;
  });

  describe('dev mode bypass (non-production)', () => {
    beforeEach(() => {
      process.env['NODE_ENV'] = 'development';
    });

    it('should return true and inject dev user when not in production', () => {
      const context = createMockContext();
      // JwtAuthGuard checks IS_PRODUCTION which is evaluated at module load time.
      // Since we loaded it in a non-production env, the bypass is active.
      const result = guard.canActivate(context);
      expect(result).toBe(true);
    });

    it('should inject a dev user with admin/analyst/operator roles', () => {
      const context = createMockContext();
      guard.canActivate(context);
      const request = context.switchToHttp().getRequest() as { user: { roles: string[]; classificationLevel: string } };
      expect(request.user).toBeDefined();
      expect(request.user.roles).toContain('admin');
      expect(request.user.roles).toContain('analyst');
      expect(request.user.roles).toContain('operator');
    });

    it('should inject a dev user with TOP_SECRET classification', () => {
      const context = createMockContext();
      guard.canActivate(context);
      const request = context.switchToHttp().getRequest() as { user: { classificationLevel: string } };
      expect(request.user.classificationLevel).toBe('TOP_SECRET');
    });
  });

  describe('role-based access control', () => {
    it('should allow access when user has required role', () => {
      const user = {
        userId: 'test-user',
        username: 'tester',
        email: 'test@test.com',
        name: 'Tester',
        roles: ['analyst'],
        realmRoles: ['analyst'],
        clientRoles: [],
        classificationLevel: 'UNCLASSIFIED',
      };

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === ROLES_KEY) return ['analyst', 'admin'];
        return undefined;
      });

      const context = createMockContext(user);
      // In dev mode the guard bypasses JWT, so we call the private method directly
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should deny access when user lacks required role', () => {
      const user = {
        userId: 'test-user',
        username: 'viewer',
        email: 'viewer@test.com',
        name: 'Viewer',
        roles: ['viewer'],
        realmRoles: ['viewer'],
        clientRoles: [],
        classificationLevel: 'UNCLASSIFIED',
      };

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === ROLES_KEY) return ['admin'];
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).toThrow(ForbiddenException);
    });

    it('should allow access when no roles are required', () => {
      const user = {
        userId: 'test-user',
        username: 'viewer',
        email: 'viewer@test.com',
        name: 'Viewer',
        roles: ['viewer'],
        realmRoles: ['viewer'],
        clientRoles: [],
        classificationLevel: 'UNCLASSIFIED',
      };

      jest.spyOn(reflector, 'getAllAndOverride').mockReturnValue(undefined);

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });
  });

  describe('classification-based access control', () => {
    it('should allow access when user clearance meets requirement', () => {
      const user = {
        userId: 'test-user',
        username: 'tester',
        email: 'test@test.com',
        name: 'Tester',
        roles: ['analyst'],
        realmRoles: ['analyst'],
        clientRoles: [],
        classificationLevel: 'TOP_SECRET',
      };

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === CLASSIFICATION_KEY) return 'SECRET';
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should deny access when user clearance is insufficient', () => {
      const user = {
        userId: 'test-user',
        username: 'tester',
        email: 'test@test.com',
        name: 'Tester',
        roles: ['analyst'],
        realmRoles: ['analyst'],
        clientRoles: [],
        classificationLevel: 'CONFIDENTIAL',
      };

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === CLASSIFICATION_KEY) return 'TOP_SECRET';
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).toThrow(ForbiddenException);
    });
  });
});
