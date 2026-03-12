import { HttpException } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { Test, TestingModule } from '@nestjs/testing';
import { KeycloakAdminService, CreateUserDto } from './keycloak-admin.service';

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
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
        .mockResolvedValueOnce(mockFetchResponseEmpty(409));

      await expect(service.createUser(validDto)).rejects.toThrow(HttpException);

      try {
        // Re-run to capture the exception
        fetchMock
          .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))
          .mockResolvedValueOnce(mockFetchResponseEmpty(409));
        await service.createUser(validDto);
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

    it('should enable user, assign roles, and remove registrationDate', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))    // getToken (cached for all subsequent calls)
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // PUT user (enable)
        .mockResolvedValueOnce(mockFetchResponse(rolesResponse))      // GET roles
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // POST role-mappings

      await expect(service.approveUser(userId)).resolves.toBeUndefined();

      // Find the PUT call to enable user
      const putCallIndex = fetchMock.mock.calls.findIndex(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes(`/users/${userId}`) &&
          call[1]?.method === 'PUT',
      );
      expect(putCallIndex).toBeGreaterThan(-1);

      const putBody = JSON.parse(fetchMock.mock.calls[putCallIndex][1].body as string);
      expect(putBody.enabled).toBe(true);
      expect(putBody.attributes?.registrationDate).toBeUndefined();
      expect(putBody.requiredActions).toContain('VERIFY_EMAIL');
    });

    it('should only assign sentinel-viewer and classification-u roles', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))    // getToken (cached)
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // PUT user (enable)
        .mockResolvedValueOnce(mockFetchResponse(rolesResponse))      // GET roles
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // POST role-mappings

      await service.approveUser(userId);

      const roleAssignCallIndex = fetchMock.mock.calls.findIndex(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes('role-mappings/realm') &&
          call[1]?.method === 'POST',
      );
      expect(roleAssignCallIndex).toBeGreaterThan(-1);

      const assignedRoles = JSON.parse(
        fetchMock.mock.calls[roleAssignCallIndex][1].body as string,
      ) as Array<{ name: string }>;

      const assignedNames = assignedRoles.map((r) => r.name);
      expect(assignedNames).toContain('sentinel-viewer');
      expect(assignedNames).toContain('classification-u');
      expect(assignedNames).not.toContain('sentinel-admin');
    });

    it('should re-disable user and throw if role fetch fails', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))    // getToken (cached)
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // PUT user (enable)
        .mockResolvedValueOnce(mockFetchResponseEmpty(500))           // GET roles fails
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // PUT re-disable

      await expect(service.approveUser(userId)).rejects.toThrow(HttpException);

      // Verify re-disable call was made
      const reDisableCall = fetchMock.mock.calls.find(
        (call) =>
          typeof call[0] === 'string' &&
          call[0].includes(`/users/${userId}`) &&
          call[1]?.method === 'PUT' &&
          (() => {
            try {
              const body = JSON.parse(call[1].body as string);
              return body.enabled === false;
            } catch {
              return false;
            }
          })(),
      );
      expect(reDisableCall).toBeDefined();
    });

    it('should re-disable user and throw if role assignment fails', async () => {
      fetchMock
        .mockResolvedValueOnce(mockFetchResponse(TOKEN_RESPONSE))    // getToken (cached)
        .mockResolvedValueOnce(mockFetchResponse(userRepresentation)) // GET user
        .mockResolvedValueOnce(mockFetchResponseEmpty(204))           // PUT user (enable)
        .mockResolvedValueOnce(mockFetchResponse(rolesResponse))      // GET roles OK
        .mockResolvedValueOnce(mockFetchResponseEmpty(500))           // POST role-mappings fails
        .mockResolvedValueOnce(mockFetchResponseEmpty(204));          // PUT re-disable

      await expect(service.approveUser(userId)).rejects.toThrow(HttpException);
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

    it('should throw INTERNAL_SERVER_ERROR when token request fails', async () => {
      fetchMock.mockResolvedValueOnce(mockFetchResponseEmpty(500));

      await expect(service.createUser(validDto)).rejects.toThrow(HttpException);
    });
  });
});
