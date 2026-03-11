import { ExecutionContext, ForbiddenException } from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import { Test, TestingModule } from '@nestjs/testing';
import { JwtAuthGuard } from './jwt-auth.guard';
import { ROLES_KEY } from './decorators/roles.decorator';
import { CLASSIFICATION_KEY } from './decorators/classification.decorator';
import { AuthAuditService } from './auth-audit.service';
import type { AuthenticatedUser } from './jwt.strategy';

function createMockContext(
  user?: Partial<AuthenticatedUser>,
  overrides?: { method?: string; url?: string },
): ExecutionContext {
  const request = {
    user,
    method: overrides?.method ?? 'GET',
    url: overrides?.url ?? '/api/test',
  } as Record<string, unknown>;
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
    switchToRpc: () => ({}) as never,
    switchToWs: () => ({}) as never,
  } as unknown as ExecutionContext;
}

function createUser(overrides?: Partial<AuthenticatedUser>): AuthenticatedUser {
  return {
    userId: 'test-user-id',
    username: 'tester',
    email: 'tester@sentinel.local',
    name: 'Test User',
    roles: ['sentinel-analyst'],
    realmRoles: ['sentinel-analyst'],
    clientRoles: [],
    classificationLevel: 'UNCLASSIFIED',
    ...overrides,
  };
}

describe('JwtAuthGuard', () => {
  let guard: JwtAuthGuard;
  let reflector: Reflector;
  let auditService: AuthAuditService;

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      providers: [
        JwtAuthGuard,
        Reflector,
        {
          provide: AuthAuditService,
          useValue: {
            logAccess: jest.fn(),
            logAccessDenied: jest.fn(),
            logRoleCheckFailed: jest.fn(),
            logClassificationCheckFailed: jest.fn(),
          },
        },
      ],
    }).compile();

    guard = module.get<JwtAuthGuard>(JwtAuthGuard);
    reflector = module.get<Reflector>(Reflector);
    auditService = module.get<AuthAuditService>(AuthAuditService);
  });

  describe('dev mode bypass (non-production)', () => {
    // IS_PRODUCTION is evaluated at module load time.
    // Since tests run in NODE_ENV=test (not 'production'), the bypass is active.

    it('should return true in non-production mode', () => {
      const context = createMockContext();
      const result = guard.canActivate(context);
      expect(result).toBe(true);
    });

    it('should inject DEV_USER onto the request', () => {
      const context = createMockContext();
      guard.canActivate(context);

      const request = context.switchToHttp().getRequest() as {
        user: AuthenticatedUser;
      };
      expect(request.user).toBeDefined();
      expect(request.user.userId).toBe(
        '00000000-0000-0000-0000-000000000001',
      );
      expect(request.user.username).toBe('dev-operator');
      expect(request.user.email).toBe('operator@sentinel.local');
    });

    it('should inject DEV_USER with all three sentinel roles', () => {
      const context = createMockContext();
      guard.canActivate(context);

      const request = context.switchToHttp().getRequest() as {
        user: AuthenticatedUser;
      };
      expect(request.user.roles).toContain('sentinel-analyst');
      expect(request.user.roles).toContain('sentinel-operator');
      expect(request.user.roles).toContain('sentinel-admin');
    });

    it('should inject DEV_USER with TOP_SECRET classification', () => {
      const context = createMockContext();
      guard.canActivate(context);

      const request = context.switchToHttp().getRequest() as {
        user: AuthenticatedUser;
      };
      expect(request.user.classificationLevel).toBe('TOP_SECRET');
    });
  });

  describe('validateRolesAndClassification (role checks)', () => {
    it('should allow access when user has one of the required roles', () => {
      const user = createUser({ roles: ['sentinel-analyst'] });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === ROLES_KEY) return ['sentinel-analyst', 'sentinel-admin'];
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should deny access and throw ForbiddenException when user lacks required role', () => {
      const user = createUser({
        username: 'viewer',
        roles: ['viewer'],
      });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === ROLES_KEY) return ['sentinel-admin'];
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).toThrow(ForbiddenException);
    });

    it('should call auditService.logRoleCheckFailed when role check fails', () => {
      const user = createUser({
        username: 'viewer',
        roles: ['viewer'],
      });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === ROLES_KEY) return ['sentinel-admin'];
        return undefined;
      });

      const context = createMockContext(user);
      try {
        (guard as any).validateRolesAndClassification(context);
      } catch {
        // expected
      }

      expect(auditService.logRoleCheckFailed).toHaveBeenCalledWith(
        user,
        expect.any(String),
        ['sentinel-admin'],
      );
    });

    it('should allow access when no roles metadata is set', () => {
      const user = createUser({ roles: ['viewer'] });

      jest.spyOn(reflector, 'getAllAndOverride').mockReturnValue(undefined);

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should allow access when roles metadata is an empty array', () => {
      const user = createUser({ roles: ['viewer'] });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === ROLES_KEY) return [];
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should throw ForbiddenException when no user is on the request', () => {
      jest.spyOn(reflector, 'getAllAndOverride').mockReturnValue(undefined);

      const context = createMockContext(undefined);
      // Clear the user that dev mode might have set
      const request = context.switchToHttp().getRequest() as Record<
        string,
        unknown
      >;
      request['user'] = undefined;

      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).toThrow(ForbiddenException);
    });
  });

  describe('validateRolesAndClassification (classification checks)', () => {
    it('should allow access when user clearance equals required level', () => {
      const user = createUser({ classificationLevel: 'SECRET' });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === CLASSIFICATION_KEY) return 'SECRET';
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should allow access when user clearance exceeds required level', () => {
      const user = createUser({ classificationLevel: 'TOP_SECRET' });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === CLASSIFICATION_KEY) return 'SECRET';
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should deny access when user clearance is below required level', () => {
      const user = createUser({ classificationLevel: 'CONFIDENTIAL' });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === CLASSIFICATION_KEY) return 'TOP_SECRET';
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).toThrow(ForbiddenException);
    });

    it('should call auditService.logClassificationCheckFailed on insufficient clearance', () => {
      const user = createUser({ classificationLevel: 'UNCLASSIFIED' });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === CLASSIFICATION_KEY) return 'SECRET';
        return undefined;
      });

      const context = createMockContext(user);
      try {
        (guard as any).validateRolesAndClassification(context);
      } catch {
        // expected
      }

      expect(auditService.logClassificationCheckFailed).toHaveBeenCalledWith(
        user,
        expect.any(String),
        'SECRET',
      );
    });

    it('should allow access when no classification metadata is set', () => {
      const user = createUser({ classificationLevel: 'UNCLASSIFIED' });

      jest.spyOn(reflector, 'getAllAndOverride').mockReturnValue(undefined);

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should default to UNCLASSIFIED when user has no classificationLevel', () => {
      const user = createUser({
        classificationLevel: undefined as unknown as string,
      });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === CLASSIFICATION_KEY) return 'CONFIDENTIAL';
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).toThrow(ForbiddenException);
    });

    it('should respect the full classification hierarchy (UNCLASSIFIED < CONFIDENTIAL < SECRET < TOP_SECRET)', () => {
      const levels = ['UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET'];

      for (let userIdx = 0; userIdx < levels.length; userIdx++) {
        for (let reqIdx = 0; reqIdx < levels.length; reqIdx++) {
          const user = createUser({ classificationLevel: levels[userIdx] });

          jest
            .spyOn(reflector, 'getAllAndOverride')
            .mockImplementation((key) => {
              if (key === CLASSIFICATION_KEY) return levels[reqIdx];
              return undefined;
            });

          const context = createMockContext(user);

          if (userIdx >= reqIdx) {
            expect(() => {
              (guard as any).validateRolesAndClassification(context);
            }).not.toThrow();
          } else {
            expect(() => {
              (guard as any).validateRolesAndClassification(context);
            }).toThrow(ForbiddenException);
          }
        }
      }
    });
  });

  describe('combined role and classification checks', () => {
    it('should allow access when user passes both role and classification checks', () => {
      const user = createUser({
        roles: ['sentinel-admin'],
        classificationLevel: 'TOP_SECRET',
      });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === ROLES_KEY) return ['sentinel-admin'];
        if (key === CLASSIFICATION_KEY) return 'SECRET';
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).not.toThrow();
    });

    it('should deny on role failure even if classification passes', () => {
      const user = createUser({
        roles: ['viewer'],
        classificationLevel: 'TOP_SECRET',
      });

      jest.spyOn(reflector, 'getAllAndOverride').mockImplementation((key) => {
        if (key === ROLES_KEY) return ['sentinel-admin'];
        if (key === CLASSIFICATION_KEY) return 'UNCLASSIFIED';
        return undefined;
      });

      const context = createMockContext(user);
      expect(() => {
        (guard as any).validateRolesAndClassification(context);
      }).toThrow(ForbiddenException);
    });
  });
});
