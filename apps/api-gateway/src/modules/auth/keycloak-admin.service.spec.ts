import { HttpException } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { Test, TestingModule } from '@nestjs/testing';
import { KeycloakAdminService, CreateUserDto, VALID_CLASSIFICATIONS } from './keycloak-admin.service';

const TOKEN_RESPONSE = {
  access_token: 'test-token',
  expires_in: 300,
};

function mockFetchResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: jest.fn().mockResolvedValue(body),
    text: jest.fn().mockResolvedValue(JSON.stringify(body)),
  } as unknown as Response;
}

function mockFetchResponseEmpty(status = 204): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: jest.fn().mockResolvedValue(null),
    text: jest.fn().mockResolvedValue(''),
  } as unknown as Response;
}

describe('KeycloakAdminService', () => {
  let service: KeycloakAdminService;
  let fetchMock: jest.Mock;

  const mockConfigValues: Record<string, string> = {
    KEYCLOAK_URL: 'http://keycloak:8080',
    KEYCLOAK_REALM: 'sentinel',
    KEYCLOAK_SERVICE_CLIENT_ID: 'sentinel-service',
    KEYCLOAK_SERVICE_CLIENT_SECRET: 'sentinel-service-secret-dev',
  };

  beforeEach(async () => {
    fetchMock = jest.fn();
    global.fetch = fetchMock;

    const module: TestingModule = await Test.createTestingModule({
      providers: [
        KeycloakAdminService,
        {
          provide: ConfigService,
          useValue: {
            get: jest.fn((key: string, defaultValue?: string) => {
              return mockConfigValues[key] ?? defaultValue;
            }),
          },
        },
      ],
    }).compile();

    service = module.get<KeycloakAdminService>(KeycloakAdminService);
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  describe('createUser', () => {
    const validDto: CreateUserDto = {
      username: 'jdoe',
      email: 'jdoe@example.com',
      firstName: 'John',
      lastName: 'Doe',
      password: 'SecurePass1!',
      organization: 'ACME Corp',
      justification: 'Need access for project Alpha',
    };

    it('should create a user successfully', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponseEmpty(201));

      await expect(service.createUser(validDto)).resolves.toBeUndefined();

      expect(fetchMock).toHaveBeenCalledTimes(2);

      // First call: token request
      const tokenCall = fetchMock.mock.calls[0];
      expect(tokenCall[0]).toContain('/protocol/openid-connect/token');
      expect(tokenCall[1].method).toBe('POST');

      // Second call: create user
      const userCall = fetchMock.mock.calls[1];
      expect(userCall[0]).toContain('/admin/realms/sentinel/users');
      expect(userCall[1].method).toBe('POST');

      const body = JSON.parse(userCall[1].body as string);
      expect(body.username).toBe('jdoe');
      expect(body.email).toBe('jdoe@example.com');
      expect(body.enabled).toBe(false);
      expect(body.attributes.organization).toEqual(['ACME Corp']);
      expect(body.attributes.justification).toEqual(['Need access for project Alpha']);
      expect(body.attributes.registrationDate).toBeDefined();
    });

    it('should throw HttpException with CONFLICT status on 409 duplicate', async () => {
      const keycloakError = { errorMessage: 'User exists with same username' };

      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponse(keycloakError, 409));

      try {
        await service.createUser(validDto);
        fail('Expected HttpException');
      } catch (err) {
        expect(err).toBeInstanceOf(HttpException);
        expect((err as HttpException).getStatus()).toBe(409);
        expect((err as HttpException).message).toBe('User exists with same username');
      }
    });

    it('should fall back to default message when 409 body has no errorMessage', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponse({}, 409));

      try {
        await service.createUser(validDto);
        fail('Expected HttpException');
      } catch (err) {
        expect(err).toBeInstanceOf(HttpException);
        expect((err as HttpException).getStatus()).toBe(409);
        expect((err as HttpException).message).toContain('already exists');
      }
    });

    it('should throw HttpException with BAD_REQUEST when password is less than 8 characters', async () => {
      const shortPasswordDto = { ...validDto, password: 'short' };

      await expect(service.createUser(shortPasswordDto)).rejects.toThrow(HttpException);

      try {
        await service.createUser(shortPasswordDto);
      } catch (err) {
        expect(err).toBeInstanceOf(HttpException);
        expect((err as HttpException).getStatus()).toBe(400);
      }

      // Fetch should not be called at all (validation before any request)
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('should include credentials in the user representation', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponseEmpty(201));

      await service.createUser(validDto);

      const body = JSON.parse(fetchMock.mock.calls[1][1].body as string);
      expect(body.credentials).toBeDefined();
      expect(body.credentials[0].type).toBe('password');
      expect(body.credentials[0].value).toBe('SecurePass1!');
    });
  });

  describe('getPendingRegistrations', () => {
    const pendingUsersResponse = [
      {
        id: 'user-1',
        username: 'jdoe',
        email: 'jdoe@example.com',
        firstName: 'John',
        lastName: 'Doe',
        attributes: {
          organization: ['ACME Corp'],
          justification: ['Project Alpha'],
          registrationDate: ['2026-03-01T10:00:00.000Z'],
        },
      },
      {
        id: 'user-2',
        username: 'jsmith',
        email: 'jsmith@example.com',
        firstName: 'Jane',
        lastName: 'Smith',
        attributes: {
          organization: ['Beta Inc'],
          justification: ['Project Beta'],
          registrationDate: ['2026-03-02T11:00:00.000Z'],
        },
      },
      {
        // Disabled user without registrationDate — should be filtered out
        id: 'user-3',
        username: 'locked-admin',
        email: 'admin@example.com',
        firstName: 'Admin',
        lastName: 'User',
        attributes: {},
      },
    ];

    it('should return pending users filtered by registrationDate attribute', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponse(pendingUsersResponse));

      const result = await service.getPendingRegistrations();

      expect(result).toHaveLength(2);
      expect(result[0].id).toBe('user-1');
      expect(result[0].username).toBe('jdoe');
      expect(result[0].organization).toBe('ACME Corp');
      expect(result[0].registrationDate).toBe('2026-03-01T10:00:00.000Z');
      expect(result[1].id).toBe('user-2');
    });

    it('should query disabled users endpoint with max=100', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponse([]));

      await service.getPendingRegistrations();

      const userCall = fetchMock.mock.calls[1];
      expect(userCall[0]).toContain('enabled=false');
      expect(userCall[0]).toContain('max=100');
    });

    it('should return empty array when no pending users exist', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponse([]));

      const result = await service.getPendingRegistrations();
      expect(result).toEqual([]);
    });

    it('should throw HttpException when API call fails', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponseEmpty(500));

      await expect(service.getPendingRegistrations()).rejects.toThrow(HttpException);
    });
  });

  describe('approveUser', () => {
    const userId = 'user-abc-123';

    const userRepresentation = {
      id: userId,
      username: 'jdoe',
      email: 'jdoe@example.com',
      firstName: 'John',
      lastName: 'Doe',
      enabled: false,
      attributes: {
        organization: ['ACME Corp'],
        justification: ['Project Alpha'],
        registrationDate: ['2026-03-01T10:00:00.000Z'],
      },
    };

    const rolesResponse = [
      { id: 'role-viewer-id', name: 'sentinel-viewer' },
      { id: 'role-class-id', name: 'classification-u' },
      { id: 'role-admin-id', name: 'sentinel-admin' },
    ];

    /**
     * Full success sequence (all fetches use the cached token from call #1):
     * 1. fetch token
     * 2. GET  /users/{id}
     * 3. PUT  /users/{id}          — enable only
     * 4. GET  /roles
     * 5. POST /users/{id}/role-mappings/realm
     * 6. PUT  /users/{id}          — remove registrationDate
     * 7. PUT  /users/{id}/execute-actions-email
     */
    function mockSuccessSequence(): void {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))    // 1. token
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // 2. GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // 3. PUT enable
        .mockResolvedValueOnce(mockFetchResponse(rolesResponse))      // 4. GET roles
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // 5. POST role-mappings
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // 6. PUT remove registrationDate
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // 7. PUT execute-actions-email
    }

    it('should resolve successfully for the full happy path', async () => {
      mockSuccessSequence();
      await expect(service.approveUser(userId)).resolves.toBeUndefined();
    });

    it('should enable user with enabled:true in the first PUT (no attribute changes)', async () => {
      mockSuccessSequence();
      await service.approveUser(userId);

      // The first PUT to /users/{id} should only set enabled:true
      const firstPutCall = fetchMock.mock.calls.find(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes(`/users/${userId}`) &&
          !call[0].includes('role-mappings') &&
          !call[0].includes('execute-actions-email') &&
          call[1]?.method === 'PUT',
      );
      expect(firstPutCall).toBeDefined();
      const firstPutBody = JSON.parse(firstPutCall![1].body as string);
      expect(firstPutBody.enabled).toBe(true);
      // Must NOT remove registrationDate at this stage (rollback safety)
      expect(firstPutBody.attributes).toBeUndefined();
    });

    it('should remove registrationDate in a separate PUT after roles are assigned', async () => {
      mockSuccessSequence();
      await service.approveUser(userId);

      // Find all PUTs to /users/{id} (excluding role-mappings and execute-actions-email)
      const userPutCalls = fetchMock.mock.calls.filter(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes(`/users/${userId}`) &&
          !call[0].includes('role-mappings') &&
          !call[0].includes('execute-actions-email') &&
          call[1]?.method === 'PUT',
      );
      // Expect exactly 2: enable-only PUT and cleanup PUT
      expect(userPutCalls).toHaveLength(2);

      const cleanupBody = JSON.parse(userPutCalls[1][1].body as string);
      expect(cleanupBody.attributes?.registrationDate).toBeUndefined();
      expect(cleanupBody.attributes?.organization).toEqual(['ACME Corp']);
    });

    it('should call execute-actions-email with VERIFY_EMAIL after roles succeed', async () => {
      mockSuccessSequence();
      await service.approveUser(userId);

      const emailCall = fetchMock.mock.calls.find(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes(`/users/${userId}/execute-actions-email`) &&
          call[1]?.method === 'PUT',
      );
      expect(emailCall).toBeDefined();
      const emailBody = JSON.parse(emailCall![1].body as string);
      expect(emailBody).toEqual(['VERIFY_EMAIL']);
    });

    it('should only assign sentinel-viewer and classification-u roles', async () => {
      mockSuccessSequence();
      await service.approveUser(userId);

      const roleAssignCall = fetchMock.mock.calls.find(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes('role-mappings/realm') &&
          call[1]?.method === 'POST',
      );
      expect(roleAssignCall).toBeDefined();

      const assignedRoles = JSON.parse(roleAssignCall![1].body as string) as Array<{ name: string }>;
      const assignedNames = assignedRoles.map((r) => r.name);
      expect(assignedNames).toContain('sentinel-viewer');
      expect(assignedNames).toContain('classification-u');
      expect(assignedNames).not.toContain('sentinel-admin');
    });

    it('should re-disable user and throw if role fetch fails, preserving registrationDate', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))    // token
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // PUT enable
        .mockResolvedValueOnce(mockFetchResponseEmpty(500))           // GET roles fails
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // PUT re-disable

      await expect(service.approveUser(userId)).rejects.toThrow(HttpException);

      // Re-disable call must set enabled:false but NOT touch attributes
      const reDisableCall = fetchMock.mock.calls.find(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes(`/users/${userId}`) &&
          !call[0].includes('role-mappings') &&
          !call[0].includes('execute-actions-email') &&
          call[1]?.method === 'PUT' &&
          (() => {
            try {
              return JSON.parse(call[1].body as string).enabled === false;
            } catch {
              return false;
            }
          })(),
      );
      expect(reDisableCall).toBeDefined();

      // The re-disable PUT must not strip registrationDate (attributes not sent at all)
      const reDisableBody = JSON.parse(reDisableCall![1].body as string);
      expect(reDisableBody.attributes).toBeUndefined();
    });

    it('should re-disable user and throw if role assignment fails', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))    // token
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // PUT enable
        .mockResolvedValueOnce(mockFetchResponse(rolesResponse))      // GET roles OK
        .mockResolvedValueOnce(mockFetchResponseEmpty(500))           // POST role-mappings fails
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // PUT re-disable

      await expect(service.approveUser(userId)).rejects.toThrow(HttpException);
    });

    it('should assign the specified classification role instead of classification-u', async () => {
      const rolesWithTs = [
        { id: 'role-viewer-id', name: 'sentinel-viewer' },
        { id: 'role-class-u-id', name: 'classification-u' },
        { id: 'role-class-ts-id', name: 'classification-ts' },
        { id: 'role-admin-id', name: 'sentinel-admin' },
      ];

      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))    // 1. token
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // 2. GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // 3. PUT enable
        .mockResolvedValueOnce(mockFetchResponse(rolesWithTs))        // 4. GET roles
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // 5. POST role-mappings
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // 6. PUT remove registrationDate
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // 7. PUT execute-actions-email

      await service.approveUser(userId, 'classification-ts');

      const roleAssignCall = fetchMock.mock.calls.find(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes('role-mappings/realm') &&
          call[1]?.method === 'POST',
      );
      expect(roleAssignCall).toBeDefined();

      const assignedRoles = JSON.parse(roleAssignCall![1].body as string) as Array<{ name: string }>;
      const assignedNames = assignedRoles.map((r) => r.name);
      expect(assignedNames).toContain('sentinel-viewer');
      expect(assignedNames).toContain('classification-ts');
      expect(assignedNames).not.toContain('classification-u');
    });

    it('should default to classification-u when classificationLevel is omitted', async () => {
      mockSuccessSequence();
      await service.approveUser(userId);

      const roleAssignCall = fetchMock.mock.calls.find(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes('role-mappings/realm') &&
          call[1]?.method === 'POST',
      );
      expect(roleAssignCall).toBeDefined();

      const assignedRoles = JSON.parse(roleAssignCall![1].body as string) as Array<{ name: string }>;
      const assignedNames = assignedRoles.map((r) => r.name);
      expect(assignedNames).toContain('classification-u');
    });

    it('should throw NOT_FOUND when user does not exist', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponseEmpty(404));

      try {
        await service.approveUser(userId);
        fail('Expected HttpException');
      } catch (err) {
        expect(err).toBeInstanceOf(HttpException);
        expect((err as HttpException).getStatus()).toBe(404);
      }
    });
  });

  describe('rejectUser', () => {
    const userId = 'user-to-reject';

    const userRepresentation = {
      id: userId,
      email: 'reject@example.com',
      firstName: 'Rejected',
      lastName: 'User',
    };

    it('should delete the user and return email and firstName', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))     // getToken (cached)
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // DELETE

      const result = await service.rejectUser(userId);

      expect(result.email).toBe('reject@example.com');
      expect(result.firstName).toBe('Rejected');

      const deleteCall = fetchMock.mock.calls.find(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes(`/users/${userId}`) &&
          call[1]?.method === 'DELETE',
      );
      expect(deleteCall).toBeDefined();
    });

    it('should throw NOT_FOUND when user does not exist', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponseEmpty(404));

      try {
        await service.rejectUser(userId);
        fail('Expected HttpException');
      } catch (err) {
        expect(err).toBeInstanceOf(HttpException);
        expect((err as HttpException).getStatus()).toBe(404);
      }
    });

    it('should throw INTERNAL_SERVER_ERROR when delete fails', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))     // getToken (cached)
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(500));          // DELETE fails

      await expect(service.rejectUser(userId)).rejects.toThrow(HttpException);
    });
  });

  describe('getActiveUsers', () => {
    const mockUsers = [
      { id: 'u1', username: 'jdoe', email: 'j@e.com', firstName: 'John', lastName: 'Doe' },
      { id: 'u2', username: 'service-account-sentinel-service', email: '', firstName: '', lastName: '' },
      { id: 'u3', username: 'viewer1', email: 'v@e.com', firstName: 'View', lastName: 'Er' },
    ];

    it('should return enabled users with their classification level', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))       // token
        .mockResolvedValueOnce(mockFetchResponse(mockUsers))             // GET /users?enabled=true&max=100
        .mockResolvedValueOnce(mockFetchResponse([                       // roles for u1
          { name: 'sentinel-viewer' }, { name: 'classification-s' }
        ]))
        .mockResolvedValueOnce(mockFetchResponse([                       // roles for u3
          { name: 'sentinel-viewer' }, { name: 'classification-u' }
        ]));

      const result = await service.getActiveUsers();

      expect(result).toHaveLength(2); // service account filtered out
      expect(result[0].username).toBe('jdoe');
      expect(result[0].classificationLevel).toBe('classification-s');
      expect(result[1].username).toBe('viewer1');
      expect(result[1].classificationLevel).toBe('classification-u');
    });

    it('should exclude service account users', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponse(mockUsers))
        .mockResolvedValueOnce(mockFetchResponse([{ name: 'sentinel-viewer' }]))
        .mockResolvedValueOnce(mockFetchResponse([{ name: 'sentinel-viewer' }]));

      const result = await service.getActiveUsers();
      expect(result.every(u => !u.username.startsWith('service-account-'))).toBe(true);
    });

    it('should return classificationLevel as null when user has no classification role', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponse([mockUsers[0]]))        // only jdoe
        .mockResolvedValueOnce(mockFetchResponse([{ name: 'sentinel-viewer' }])); // no classification

      const result = await service.getActiveUsers();
      expect(result[0].classificationLevel).toBeNull();
    });

    it('should throw when the users endpoint fails', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponseEmpty(500));

      await expect(service.getActiveUsers()).rejects.toThrow(HttpException);
    });
  });

  describe('token caching', () => {
    const validDto: CreateUserDto = {
      username: 'cache-test',
      email: 'cache@example.com',
      firstName: 'Cache',
      lastName: 'Test',
      password: 'CachePass1!',
      organization: 'Cache Corp',
      justification: 'Testing caching',
    };

    it('should cache the token and not re-fetch it on subsequent calls', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))   // token (1st call)
        .mockResolvedValueOnce(mockFetchResponseEmpty(201))          // create user (1st call)
        .mockResolvedValueOnce(mockFetchResponseEmpty(201));         // create user (2nd call, no new token)

      await service.createUser(validDto);
      await service.createUser({ ...validDto, username: 'cache-test-2', email: 'cache2@example.com' });

      // Token endpoint should only have been called once
      const tokenCalls = fetchMock.mock.calls.filter(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes('/protocol/openid-connect/token'),
      );
      expect(tokenCalls).toHaveLength(1);
    });

    it('should refresh token when it is within 30 seconds of expiry', async () => {
      // First fetch: short-lived token (expires in 25 seconds — within the 30s buffer)
      const shortLivedToken = { access_token: 'short-token', expires_in: 25 };
      const freshToken = { access_token: 'fresh-token', expires_in: 300 };

      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(shortLivedToken))   // token (1st call)
        .mockResolvedValueOnce(mockFetchResponseEmpty(201))           // create user (1st call)
        .mockResolvedValueOnce(mockFetchResponse(freshToken))         // token refresh (2nd call)
        .mockResolvedValueOnce(mockFetchResponseEmpty(201));          // create user (2nd call)

      await service.createUser(validDto);
      await service.createUser({ ...validDto, username: 'cache-test-3', email: 'cache3@example.com' });

      // Token endpoint should have been called twice (refresh triggered)
      const tokenCalls = fetchMock.mock.calls.filter(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes('/protocol/openid-connect/token'),
      );
      expect(tokenCalls).toHaveLength(2);

      // Second create user call should use the fresh token
      const secondUserCall = fetchMock.mock.calls.filter(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes('/admin/realms/sentinel/users') &&
          call[1]?.method === 'POST',
      )[1];
      expect(secondUserCall[1].headers['Authorization']).toBe('Bearer fresh-token');
    });

    it('should throw SERVICE_UNAVAILABLE (503) when token request fails', async () => {
      fetchMock.mockResolvedValueOnce(mockFetchResponseEmpty(500));

      try {
        await service.createUser(validDto);
        fail('Expected HttpException');
      } catch (err) {
        expect(err).toBeInstanceOf(HttpException);
        expect((err as HttpException).getStatus()).toBe(503);
      }
    });
  });
});
