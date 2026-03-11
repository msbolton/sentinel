import { Logger } from '@nestjs/common';
import { Test, TestingModule } from '@nestjs/testing';
import { AuthAuditService } from './auth-audit.service';
import type { AuthenticatedUser } from './jwt.strategy';

describe('AuthAuditService', () => {
  let service: AuthAuditService;
  let logSpy: jest.SpyInstance;
  let warnSpy: jest.SpyInstance;

  const mockUser: AuthenticatedUser = {
    userId: 'user-123',
    username: 'analyst-jane',
    email: 'jane@sentinel.local',
    name: 'Jane Analyst',
    roles: ['sentinel-analyst'],
    realmRoles: ['sentinel-analyst'],
    clientRoles: [],
    classificationLevel: 'SECRET',
  };

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      providers: [AuthAuditService],
    }).compile();

    service = module.get<AuthAuditService>(AuthAuditService);

    // Spy on Logger prototype methods to capture log output
    logSpy = jest.spyOn(Logger.prototype, 'log').mockImplementation();
    warnSpy = jest.spyOn(Logger.prototype, 'warn').mockImplementation();
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  describe('logAccess', () => {
    it('should log an access event without throwing', () => {
      expect(() => {
        service.logAccess(mockUser, 'GET', '/api/entities', '192.168.1.1');
      }).not.toThrow();
    });

    it('should call logger.log with a JSON string containing event details', () => {
      service.logAccess(mockUser, 'GET', '/api/entities', '10.0.0.1');

      expect(logSpy).toHaveBeenCalledTimes(1);
      const loggedJson = JSON.parse(logSpy.mock.calls[0][0]);
      expect(loggedJson.eventType).toBe('ACCESS');
      expect(loggedJson.userId).toBe('user-123');
      expect(loggedJson.username).toBe('analyst-jane');
      expect(loggedJson.method).toBe('GET');
      expect(loggedJson.resource).toBe('/api/entities');
      expect(loggedJson.ip).toBe('10.0.0.1');
      expect(loggedJson.timestamp).toBeDefined();
    });

    it('should handle undefined IP gracefully', () => {
      expect(() => {
        service.logAccess(mockUser, 'POST', '/api/tracks');
      }).not.toThrow();

      const loggedJson = JSON.parse(logSpy.mock.calls[0][0]);
      expect(loggedJson.ip).toBeUndefined();
    });
  });

  describe('logAccessDenied', () => {
    it('should log a warning without throwing', () => {
      expect(() => {
        service.logAccessDenied(
          mockUser,
          'DELETE',
          '/api/admin/purge',
          'Insufficient permissions',
        );
      }).not.toThrow();
    });

    it('should call logger.warn with ACCESS_DENIED event type', () => {
      service.logAccessDenied(
        mockUser,
        'DELETE',
        '/api/admin/purge',
        'Insufficient permissions',
      );

      expect(warnSpy).toHaveBeenCalledTimes(1);
      const loggedJson = JSON.parse(warnSpy.mock.calls[0][0]);
      expect(loggedJson.eventType).toBe('ACCESS_DENIED');
      expect(loggedJson.reason).toBe('Insufficient permissions');
      expect(loggedJson.userId).toBe('user-123');
    });

    it('should handle undefined user gracefully', () => {
      expect(() => {
        service.logAccessDenied(
          undefined,
          'GET',
          '/api/secret',
          'No token provided',
        );
      }).not.toThrow();

      const loggedJson = JSON.parse(warnSpy.mock.calls[0][0]);
      expect(loggedJson.userId).toBeUndefined();
      expect(loggedJson.username).toBeUndefined();
    });

    it('should merge additional detail fields', () => {
      service.logAccessDenied(mockUser, 'GET', '/api/data', 'Denied', {
        requiredRoles: ['admin'],
        userRoles: ['analyst'],
      });

      const loggedJson = JSON.parse(warnSpy.mock.calls[0][0]);
      expect(loggedJson.requiredRoles).toEqual(['admin']);
      expect(loggedJson.userRoles).toEqual(['analyst']);
    });
  });

  describe('logRoleCheckFailed', () => {
    it('should log a warning without throwing', () => {
      expect(() => {
        service.logRoleCheckFailed(mockUser, '/api/admin', [
          'sentinel-admin',
        ]);
      }).not.toThrow();
    });

    it('should call logger.warn with ROLE_CHECK_FAILED event type', () => {
      service.logRoleCheckFailed(mockUser, '/api/admin', [
        'sentinel-admin',
        'sentinel-operator',
      ]);

      expect(warnSpy).toHaveBeenCalledTimes(1);
      const loggedJson = JSON.parse(warnSpy.mock.calls[0][0]);
      expect(loggedJson.eventType).toBe('ROLE_CHECK_FAILED');
      expect(loggedJson.userId).toBe('user-123');
      expect(loggedJson.username).toBe('analyst-jane');
      expect(loggedJson.resource).toBe('/api/admin');
      expect(loggedJson.requiredRoles).toEqual([
        'sentinel-admin',
        'sentinel-operator',
      ]);
      expect(loggedJson.userRoles).toEqual(['sentinel-analyst']);
      expect(loggedJson.timestamp).toBeDefined();
    });
  });

  describe('logClassificationCheckFailed', () => {
    it('should log a warning without throwing', () => {
      expect(() => {
        service.logClassificationCheckFailed(
          mockUser,
          '/api/sigint',
          'TOP_SECRET',
        );
      }).not.toThrow();
    });

    it('should call logger.warn with CLASSIFICATION_CHECK_FAILED event type', () => {
      service.logClassificationCheckFailed(
        mockUser,
        '/api/sigint',
        'TOP_SECRET',
      );

      expect(warnSpy).toHaveBeenCalledTimes(1);
      const loggedJson = JSON.parse(warnSpy.mock.calls[0][0]);
      expect(loggedJson.eventType).toBe('CLASSIFICATION_CHECK_FAILED');
      expect(loggedJson.userId).toBe('user-123');
      expect(loggedJson.username).toBe('analyst-jane');
      expect(loggedJson.resource).toBe('/api/sigint');
      expect(loggedJson.requiredClassification).toBe('TOP_SECRET');
      expect(loggedJson.userClassification).toBe('SECRET');
      expect(loggedJson.timestamp).toBeDefined();
    });
  });
});
