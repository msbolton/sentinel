# Registration Workflow Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add self-service registration with admin approval to Sentinel, backed by Keycloak's Admin REST API.

**Architecture:** New NestJS `RegistrationController` + `KeycloakAdminService` handle user creation/approval/rejection via Keycloak Admin API. New Angular `RegisterComponent` and `PendingUsersComponent` provide the UI. MailHog handles dev email. `@nestjs/throttler` rate-limits the public registration endpoint.

**Tech Stack:** Angular 19 (standalone components, signals, zoneless), NestJS (Passport, `@nestjs/throttler`, `@nestjs/mailer`), Keycloak 24 Admin REST API, MailHog (dev SMTP)

**Spec:** `docs/superpowers/specs/2026-03-11-registration-workflow-design.md`

---

## Chunk 1: Infrastructure & Backend Foundation

### Task 1: Install dependencies

**Files:**
- Modify: `package.json`

- [ ] **Step 1: Install npm packages**

```bash
npm install @nestjs/throttler@^5 @nestjs/mailer nodemailer
npm install -D @types/nodemailer
```

- [ ] **Step 2: Verify packages installed**

```bash
grep -E "@nestjs/throttler|@nestjs/mailer|nodemailer" package.json
```

Expected: All three packages appear in dependencies/devDependencies.

- [ ] **Step 3: Commit**

```bash
git add package.json package-lock.json
git commit -m "chore: add @nestjs/throttler, @nestjs/mailer, nodemailer dependencies"
```

---

### Task 2: Add MailHog to docker-compose and SMTP config to Keycloak realm

**Files:**
- Modify: `docker-compose.infra.yml:391` (before volumes section)
- Modify: `config/keycloak/sentinel-realm.json`

- [ ] **Step 1: Add MailHog service to `docker-compose.infra.yml`**

Insert before the `# Volumes` section (line 392):

```yaml
  # ===========================================================================
  # MailHog (Dev Email Catcher)
  # ===========================================================================
  mailhog:
    image: mailhog/mailhog:latest
    container_name: sentinel-mailhog
    hostname: mailhog
    restart: unless-stopped
    ports:
      - "1025:1025"
      - "8025:8025"
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:8025/ > /dev/null 2>&1 || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 3
      start_period: 5s
    deploy:
      resources:
        limits:
          memory: 128m
          cpus: "0.25"
    networks:
      - sentinel-network
```

- [ ] **Step 2: Add SMTP config to `sentinel-realm.json`**

Add after the `"sslRequired": "external"` line (line 4) — insert a new `"smtpServer"` block:

```json
"smtpServer": {
  "host": "mailhog",
  "port": "1025",
  "from": "sentinel@sentinel.local",
  "fromDisplayName": "SENTINEL Platform",
  "ssl": "false",
  "starttls": "false",
  "auth": "false"
},
```

- [ ] **Step 3: Add `manage-users` role mapping for service account**

Add a service account user entry to the `"users"` array in `sentinel-realm.json`. This grants the `sentinel-service` client's service account the `manage-users` role from the `realm-management` client:

```json
{
  "username": "service-account-sentinel-service",
  "enabled": true,
  "serviceAccountClientId": "sentinel-service",
  "clientRoles": {
    "realm-management": ["manage-users"]
  }
}
```

Note: In Keycloak 24's realm import format, service account role mappings are defined as user entries with `serviceAccountClientId` and `clientRoles`, not as a top-level key.

- [ ] **Step 4: Verify docker-compose syntax**

```bash
docker-compose -f docker-compose.infra.yml config --quiet
```

Expected: No errors.

- [ ] **Step 5: Commit**

```bash
git add docker-compose.infra.yml config/keycloak/sentinel-realm.json
git commit -m "infra: add MailHog service and Keycloak SMTP config for registration emails"
```

---

### Task 3: Create `KeycloakAdminService`

**Files:**
- Create: `apps/api-gateway/src/modules/auth/keycloak-admin.service.ts`
- Create: `apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts`

- [ ] **Step 1: Write the failing tests**

Create `apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts`:

```typescript
import { Test, TestingModule } from '@nestjs/testing';
import { ConfigService } from '@nestjs/config';
import { KeycloakAdminService } from './keycloak-admin.service';

// Mock global fetch
const mockFetch = jest.fn();
global.fetch = mockFetch;

function mockTokenResponse() {
  return {
    ok: true,
    json: () => Promise.resolve({ access_token: 'test-token', expires_in: 300 }),
  };
}

describe('KeycloakAdminService', () => {
  let service: KeycloakAdminService;
  let configService: ConfigService;

  beforeEach(async () => {
    jest.clearAllMocks();

    const module: TestingModule = await Test.createTestingModule({
      providers: [
        KeycloakAdminService,
        {
          provide: ConfigService,
          useValue: {
            get: jest.fn((key: string) => {
              const config: Record<string, string> = {
                'KEYCLOAK_URL': 'http://keycloak:8080',
                'KEYCLOAK_REALM': 'sentinel',
                'KEYCLOAK_SERVICE_CLIENT_ID': 'sentinel-service',
                'KEYCLOAK_SERVICE_CLIENT_SECRET': 'sentinel-service-secret-dev',
              };
              return config[key];
            }),
          },
        },
      ],
    }).compile();

    service = module.get<KeycloakAdminService>(KeycloakAdminService);
    configService = module.get<ConfigService>(ConfigService);
  });

  describe('createUser', () => {
    it('should obtain a token and create a disabled user in Keycloak', async () => {
      mockFetch
        .mockResolvedValueOnce(mockTokenResponse()) // token request
        .mockResolvedValueOnce({ ok: true, status: 201, headers: { get: () => '/users/new-user-id' } }); // create user

      await service.createUser({
        username: 'testuser',
        email: 'test@example.com',
        firstName: 'Test',
        lastName: 'User',
        password: 'Password1!',
        organization: 'Test Org',
        justification: 'Need access',
      });

      // Second fetch call is the create user request
      const createCall = mockFetch.mock.calls[1];
      expect(createCall[0]).toContain('/admin/realms/sentinel/users');
      const body = JSON.parse(createCall[1].body);
      expect(body.username).toBe('testuser');
      expect(body.enabled).toBe(false);
      expect(body.attributes.organization).toEqual(['Test Org']);
      expect(body.attributes.justification).toEqual(['Need access']);
      expect(body.attributes.registrationDate).toBeDefined();
    });

    it('should throw on duplicate user (409)', async () => {
      mockFetch
        .mockResolvedValueOnce(mockTokenResponse())
        .mockResolvedValueOnce({ ok: false, status: 409, json: () => Promise.resolve({ errorMessage: 'User exists' }) });

      await expect(service.createUser({
        username: 'existing',
        email: 'existing@example.com',
        firstName: 'E',
        lastName: 'X',
        password: 'Password1!',
        organization: 'Org',
        justification: 'Reason',
      })).rejects.toThrow('User exists');
    });
  });

  describe('getPendingRegistrations', () => {
    it('should return disabled users with registrationDate attribute', async () => {
      const mockUsers = [
        {
          id: 'u1',
          username: 'pending1',
          email: 'p1@test.com',
          firstName: 'Pending',
          lastName: 'One',
          enabled: false,
          attributes: { registrationDate: ['2026-03-11T00:00:00Z'], organization: ['Org1'], justification: ['Need it'] },
        },
      ];

      mockFetch
        .mockResolvedValueOnce(mockTokenResponse())
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve(mockUsers) });

      const result = await service.getPendingRegistrations();

      expect(result).toHaveLength(1);
      expect(result[0].username).toBe('pending1');
    });
  });

  describe('approveUser', () => {
    it('should enable user, assign roles, remove registrationDate, and trigger verify email', async () => {
      // Token, get user, enable user, get roles, assign viewer role, assign classification role, remove attribute, execute actions
      mockFetch
        .mockResolvedValueOnce(mockTokenResponse())
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ id: 'u1', attributes: { registrationDate: ['2026-03-11'], organization: ['Org'] } }) }) // get user
        .mockResolvedValueOnce({ ok: true }) // enable
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve([{ id: 'role-viewer-id', name: 'sentinel-viewer' }, { id: 'role-cu-id', name: 'classification-u' }]) }) // get available roles
        .mockResolvedValueOnce({ ok: true }) // assign roles
        .mockResolvedValueOnce({ ok: true }) // update attributes (remove registrationDate)
        .mockResolvedValueOnce({ ok: true }); // execute actions (verify email)

      await service.approveUser('u1');

      // Verify enable call sets enabled: true
      const enableCall = mockFetch.mock.calls[2];
      expect(enableCall[0]).toContain('/users/u1');
      expect(JSON.parse(enableCall[1].body).enabled).toBe(true);
    });
  });

  describe('rejectUser', () => {
    it('should fetch user info then delete the user', async () => {
      mockFetch
        .mockResolvedValueOnce(mockTokenResponse())
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ id: 'u1', email: 'rejected@test.com', firstName: 'R' }) }) // get user
        .mockResolvedValueOnce({ ok: true }); // delete user

      const result = await service.rejectUser('u1');

      expect(result.email).toBe('rejected@test.com');
      const deleteCall = mockFetch.mock.calls[2];
      expect(deleteCall[0]).toContain('/users/u1');
      expect(deleteCall[1].method).toBe('DELETE');
    });
  });

  describe('token caching', () => {
    it('should reuse cached token for subsequent requests', async () => {
      mockFetch
        .mockResolvedValueOnce(mockTokenResponse()) // first token
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve([]) }) // first API call
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve([]) }); // second API call (no new token)

      await service.getPendingRegistrations();
      await service.getPendingRegistrations();

      // Only one token request (first call), not two
      const tokenCalls = mockFetch.mock.calls.filter(
        (call: [string, ...unknown[]]) => call[0].includes('token')
      );
      expect(tokenCalls).toHaveLength(1);
    });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
npx nx test api-gateway --testFile=src/modules/auth/keycloak-admin.service.spec.ts
```

Expected: FAIL — `Cannot find module './keycloak-admin.service'`

- [ ] **Step 3: Write the implementation**

Create `apps/api-gateway/src/modules/auth/keycloak-admin.service.ts`:

```typescript
import { Injectable, Logger, HttpException, HttpStatus, OnModuleInit } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';

export interface CreateUserDto {
  username: string;
  email: string;
  firstName: string;
  lastName: string;
  password: string;
  organization: string;
  justification: string;
}

export interface PendingUser {
  id: string;
  username: string;
  email: string;
  firstName: string;
  lastName: string;
  organization: string;
  justification: string;
  registrationDate: string;
}

interface KeycloakUser {
  id: string;
  username: string;
  email: string;
  firstName: string;
  lastName: string;
  enabled: boolean;
  attributes?: Record<string, string[]>;
}

@Injectable()
export class KeycloakAdminService {
  private readonly logger = new Logger(KeycloakAdminService.name);
  private readonly keycloakUrl: string;
  private readonly realm: string;
  private readonly clientId: string;
  private readonly clientSecret: string;

  private cachedToken: string | null = null;
  private tokenExpiresAt = 0;

  constructor(private readonly configService: ConfigService) {
    this.keycloakUrl = this.configService.get<string>('KEYCLOAK_URL') ?? 'http://localhost:8080';
    this.realm = this.configService.get<string>('KEYCLOAK_REALM') ?? 'sentinel';
    this.clientId = this.configService.get<string>('KEYCLOAK_SERVICE_CLIENT_ID') ?? 'sentinel-service';
    this.clientSecret = this.configService.get<string>('KEYCLOAK_SERVICE_CLIENT_SECRET') ?? 'sentinel-service-secret-dev';
  }

  private get adminBaseUrl(): string {
    return `${this.keycloakUrl}/admin/realms/${this.realm}`;
  }

  private async getToken(): Promise<string> {
    const now = Date.now();
    if (this.cachedToken && now < this.tokenExpiresAt) {
      return this.cachedToken;
    }

    const tokenUrl = `${this.keycloakUrl}/realms/${this.realm}/protocol/openid-connect/token`;
    const body = new URLSearchParams({
      grant_type: 'client_credentials',
      client_id: this.clientId,
      client_secret: this.clientSecret,
    });

    const response = await fetch(tokenUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    });

    if (!response.ok) {
      this.logger.error('Failed to obtain Keycloak admin token');
      throw new HttpException('Authentication service unavailable', HttpStatus.SERVICE_UNAVAILABLE);
    }

    const data = await response.json();
    this.cachedToken = data.access_token;
    // Refresh 30 seconds before expiry
    this.tokenExpiresAt = now + (data.expires_in - 30) * 1000;
    return this.cachedToken!;
  }

  private async adminRequest(path: string, options: RequestInit = {}): Promise<Response> {
    const token = await this.getToken();
    return fetch(`${this.adminBaseUrl}${path}`, {
      ...options,
      headers: {
        'Authorization': `Bearer ${token}`,
        'Content-Type': 'application/json',
        ...options.headers,
      },
    });
  }

  async createUser(dto: CreateUserDto): Promise<void> {
    if (dto.password.length < 8) {
      throw new HttpException('Password must be at least 8 characters', HttpStatus.BAD_REQUEST);
    }

    const keycloakUser = {
      username: dto.username,
      email: dto.email,
      firstName: dto.firstName,
      lastName: dto.lastName,
      enabled: false,
      attributes: {
        organization: [dto.organization],
        justification: [dto.justification],
        registrationDate: [new Date().toISOString()],
      },
      credentials: [
        {
          type: 'password',
          value: dto.password,
          temporary: false,
        },
      ],
    };

    const response = await this.adminRequest('/users', {
      method: 'POST',
      body: JSON.stringify(keycloakUser),
    });

    if (response.status === 409) {
      const err = await response.json().catch(() => null);
      throw new HttpException(
        err?.errorMessage ?? 'A user with this username or email already exists',
        HttpStatus.CONFLICT,
      );
    }

    if (!response.ok) {
      const err = await response.json().catch(() => null);
      this.logger.error(`Failed to create user: ${err?.errorMessage ?? response.statusText}`);
      throw new HttpException(
        err?.errorMessage ?? 'Failed to create user',
        HttpStatus.BAD_REQUEST,
      );
    }
  }

  async getPendingRegistrations(): Promise<PendingUser[]> {
    const response = await this.adminRequest('/users?enabled=false&max=100');

    if (!response.ok) {
      throw new HttpException('Failed to fetch pending registrations', HttpStatus.SERVICE_UNAVAILABLE);
    }

    const users: KeycloakUser[] = await response.json();

    return users
      .filter((u) => u.attributes?.registrationDate?.length)
      .map((u) => ({
        id: u.id,
        username: u.username,
        email: u.email,
        firstName: u.firstName,
        lastName: u.lastName,
        organization: u.attributes?.organization?.[0] ?? '',
        justification: u.attributes?.justification?.[0] ?? '',
        registrationDate: u.attributes?.registrationDate?.[0] ?? '',
      }));
  }

  async approveUser(userId: string): Promise<void> {
    // Get user to read current attributes
    const getUserResp = await this.adminRequest(`/users/${userId}`);
    if (!getUserResp.ok) {
      throw new HttpException('User not found', HttpStatus.NOT_FOUND);
    }
    const user: KeycloakUser = await getUserResp.json();

    // Enable the user
    const enableResp = await this.adminRequest(`/users/${userId}`, {
      method: 'PUT',
      body: JSON.stringify({ ...user, enabled: true }),
    });
    if (!enableResp.ok) {
      throw new HttpException('Failed to enable user', HttpStatus.INTERNAL_SERVER_ERROR);
    }

    // Get available realm roles and assign defaults
    const rolesResp = await this.adminRequest('/roles');
    if (!rolesResp.ok) {
      throw new HttpException('Failed to fetch roles for assignment', HttpStatus.INTERNAL_SERVER_ERROR);
    }
    const allRoles = await rolesResp.json();
    const rolesToAssign = allRoles.filter(
      (r: { name: string }) => r.name === 'sentinel-viewer' || r.name === 'classification-u',
    );
    if (rolesToAssign.length > 0) {
      const assignResp = await this.adminRequest(`/users/${userId}/role-mappings/realm`, {
        method: 'POST',
        body: JSON.stringify(rolesToAssign),
      });
      if (!assignResp.ok) {
        // Roll back: re-disable the user so they don't have access without roles
        await this.adminRequest(`/users/${userId}`, {
          method: 'PUT',
          body: JSON.stringify({ ...user, enabled: false }),
        });
        throw new HttpException('Failed to assign roles, user re-disabled', HttpStatus.INTERNAL_SERVER_ERROR);
      }
    }

    // Remove registrationDate attribute (keep other attributes)
    const updatedAttributes = { ...user.attributes };
    delete updatedAttributes?.registrationDate;
    await this.adminRequest(`/users/${userId}`, {
      method: 'PUT',
      body: JSON.stringify({ ...user, enabled: true, attributes: updatedAttributes }),
    });

    // Trigger verify email action
    await this.adminRequest(`/users/${userId}/execute-actions-email`, {
      method: 'PUT',
      body: JSON.stringify(['VERIFY_EMAIL']),
    });
  }

  async rejectUser(userId: string): Promise<{ email: string; firstName: string }> {
    // Get user info for the rejection email
    const getUserResp = await this.adminRequest(`/users/${userId}`);
    if (!getUserResp.ok) {
      throw new HttpException('User not found', HttpStatus.NOT_FOUND);
    }
    const user: KeycloakUser = await getUserResp.json();

    // Delete the user
    const deleteResp = await this.adminRequest(`/users/${userId}`, {
      method: 'DELETE',
    });
    if (!deleteResp.ok) {
      throw new HttpException('Failed to delete user', HttpStatus.INTERNAL_SERVER_ERROR);
    }

    return { email: user.email, firstName: user.firstName };
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
npx nx test api-gateway --testFile=src/modules/auth/keycloak-admin.service.spec.ts
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/auth/keycloak-admin.service.ts apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts
git commit -m "feat(auth): add KeycloakAdminService for user management via Admin API"
```

---

### Task 4: Create `RegistrationController`

**Files:**
- Create: `apps/api-gateway/src/modules/auth/registration.controller.ts`
- Create: `apps/api-gateway/src/modules/auth/registration.controller.spec.ts`

- [ ] **Step 1: Write the failing tests**

Create `apps/api-gateway/src/modules/auth/registration.controller.spec.ts`:

```typescript
import { Test, TestingModule } from '@nestjs/testing';
import { HttpException, HttpStatus } from '@nestjs/common';
import { ThrottlerModule } from '@nestjs/throttler';
import { RegistrationController } from './registration.controller';
import { KeycloakAdminService } from './keycloak-admin.service';
import { MailerService } from '@nestjs/mailer';

describe('RegistrationController', () => {
  let controller: RegistrationController;
  let keycloakAdmin: {
    createUser: jest.Mock;
    getPendingRegistrations: jest.Mock;
    approveUser: jest.Mock;
    rejectUser: jest.Mock;
  };
  let mailerService: { sendMail: jest.Mock };

  beforeEach(async () => {
    keycloakAdmin = {
      createUser: jest.fn().mockResolvedValue(undefined),
      getPendingRegistrations: jest.fn().mockResolvedValue([]),
      approveUser: jest.fn().mockResolvedValue(undefined),
      rejectUser: jest.fn().mockResolvedValue({ email: 'user@test.com', firstName: 'Test' }),
    };
    mailerService = { sendMail: jest.fn().mockResolvedValue(undefined) };

    const module: TestingModule = await Test.createTestingModule({
      imports: [ThrottlerModule.forRoot([{ ttl: 1, limit: 100 }])],
      controllers: [RegistrationController],
      providers: [
        { provide: KeycloakAdminService, useValue: keycloakAdmin },
        { provide: MailerService, useValue: mailerService },
      ],
    }).compile();

    controller = module.get<RegistrationController>(RegistrationController);
  });

  describe('POST /register', () => {
    const validDto = {
      username: 'newuser',
      email: 'new@test.com',
      password: 'Password1!',
      confirmPassword: 'Password1!',
      firstName: 'New',
      lastName: 'User',
      organization: 'Test Org',
      justification: 'Need access for analysis',
    };

    it('should create a user and return success message', async () => {
      const result = await controller.register(validDto);

      expect(keycloakAdmin.createUser).toHaveBeenCalledWith({
        username: 'newuser',
        email: 'new@test.com',
        password: 'Password1!',
        firstName: 'New',
        lastName: 'User',
        organization: 'Test Org',
        justification: 'Need access for analysis',
      });
      expect(result.message).toContain('submitted');
    });

    it('should reject when passwords do not match', async () => {
      await expect(
        controller.register({ ...validDto, confirmPassword: 'wrong' }),
      ).rejects.toThrow(HttpException);
    });

    it('should reject when password is too short', async () => {
      await expect(
        controller.register({ ...validDto, password: 'short', confirmPassword: 'short' }),
      ).rejects.toThrow(HttpException);
    });
  });

  describe('GET /pending-registrations', () => {
    it('should return list of pending users', async () => {
      const pending = [{ id: 'u1', username: 'pending1' }];
      keycloakAdmin.getPendingRegistrations.mockResolvedValue(pending);

      const result = await controller.getPendingRegistrations();
      expect(result).toEqual(pending);
    });
  });

  describe('POST /approve-registration/:userId', () => {
    it('should approve the user', async () => {
      const result = await controller.approveRegistration('user-id-1');

      expect(keycloakAdmin.approveUser).toHaveBeenCalledWith('user-id-1');
      expect(result.message).toContain('approved');
    });
  });

  describe('POST /reject-registration/:userId', () => {
    it('should reject the user and send rejection email', async () => {
      const result = await controller.rejectRegistration('user-id-1');

      expect(keycloakAdmin.rejectUser).toHaveBeenCalledWith('user-id-1');
      expect(mailerService.sendMail).toHaveBeenCalledWith(
        expect.objectContaining({ to: 'user@test.com' }),
      );
      expect(result.message).toContain('rejected');
    });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
npx nx test api-gateway --testFile=src/modules/auth/registration.controller.spec.ts
```

Expected: FAIL — `Cannot find module './registration.controller'`

- [ ] **Step 3: Write the implementation**

Create `apps/api-gateway/src/modules/auth/registration.controller.ts`:

```typescript
import {
  Controller,
  Post,
  Get,
  Body,
  Param,
  UseGuards,
  HttpException,
  HttpStatus,
  Logger,
} from '@nestjs/common';
import { Throttle } from '@nestjs/throttler';
import { KeycloakAdminService } from './keycloak-admin.service';
import { JwtAuthGuard } from './jwt-auth.guard';
import { Roles } from './decorators/roles.decorator';
import { MailerService } from '@nestjs/mailer';

interface RegisterDto {
  username: string;
  email: string;
  password: string;
  confirmPassword: string;
  firstName: string;
  lastName: string;
  organization: string;
  justification: string;
}

@Controller('auth')
export class RegistrationController {
  private readonly logger = new Logger(RegistrationController.name);

  constructor(
    private readonly keycloakAdmin: KeycloakAdminService,
    private readonly mailerService: MailerService,
  ) {}

  @Post('register')
  @Throttle({ default: { limit: 5, ttl: 900000 } })
  async register(@Body() dto: RegisterDto): Promise<{ message: string }> {
    if (dto.password !== dto.confirmPassword) {
      throw new HttpException('Passwords do not match', HttpStatus.BAD_REQUEST);
    }

    if (dto.password.length < 8) {
      throw new HttpException('Password must be at least 8 characters', HttpStatus.BAD_REQUEST);
    }

    await this.keycloakAdmin.createUser({
      username: dto.username,
      email: dto.email,
      password: dto.password,
      firstName: dto.firstName,
      lastName: dto.lastName,
      organization: dto.organization,
      justification: dto.justification,
    });

    this.logger.log(`Registration request submitted for user: ${dto.username}`);

    return {
      message: 'Your request has been submitted. You will receive an email when your account is approved.',
    };
  }

  @Get('pending-registrations')
  @UseGuards(JwtAuthGuard)
  @Roles('sentinel-admin')
  async getPendingRegistrations() {
    return this.keycloakAdmin.getPendingRegistrations();
  }

  @Post('approve-registration/:userId')
  @UseGuards(JwtAuthGuard)
  @Roles('sentinel-admin')
  async approveRegistration(@Param('userId') userId: string): Promise<{ message: string }> {
    await this.keycloakAdmin.approveUser(userId);
    this.logger.log(`User ${userId} approved`);
    return { message: 'User approved successfully' };
  }

  @Post('reject-registration/:userId')
  @UseGuards(JwtAuthGuard)
  @Roles('sentinel-admin')
  async rejectRegistration(@Param('userId') userId: string): Promise<{ message: string }> {
    const { email, firstName } = await this.keycloakAdmin.rejectUser(userId);

    await this.mailerService.sendMail({
      to: email,
      subject: 'SENTINEL - Access Request Update',
      text: `Hello ${firstName},\n\nYour Sentinel access request was not approved. Contact your administrator for details.\n\nSENTINEL Platform`,
    });

    this.logger.log(`User ${userId} rejected, notification sent to ${email}`);
    return { message: 'User rejected and notification sent' };
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
npx nx test api-gateway --testFile=src/modules/auth/registration.controller.spec.ts
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/auth/registration.controller.ts apps/api-gateway/src/modules/auth/registration.controller.spec.ts
git commit -m "feat(auth): add RegistrationController with register, approve, reject endpoints"
```

---

### Task 5: Register new services in `AuthModule`

**Files:**
- Modify: `apps/api-gateway/src/modules/auth/auth.module.ts`

- [ ] **Step 1: Update `auth.module.ts`**

Replace the entire file content with:

```typescript
import { Module } from '@nestjs/common';
import { APP_GUARD, APP_INTERCEPTOR } from '@nestjs/core';
import { PassportModule } from '@nestjs/passport';
import { ConfigService } from '@nestjs/config';
import { ThrottlerModule, ThrottlerGuard } from '@nestjs/throttler';
import { MailerModule } from '@nestjs/mailer';
import { JwtStrategy } from './jwt.strategy';
import { JwtAuthGuard } from './jwt-auth.guard';
import { AuthAuditService } from './auth-audit.service';
import { AuthPropagationInterceptor } from './auth-propagation.interceptor';
import { KeycloakAdminService } from './keycloak-admin.service';
import { RegistrationController } from './registration.controller';

@Module({
  imports: [
    PassportModule.register({ defaultStrategy: 'jwt' }),
    ThrottlerModule.forRoot([{
      ttl: 900000,
      limit: 5,
    }]),
    MailerModule.forRootAsync({
      inject: [ConfigService],
      useFactory: (config: ConfigService) => ({
        transport: {
          host: config.get<string>('MAIL_HOST') ?? 'localhost',
          port: parseInt(config.get<string>('MAIL_PORT') ?? '1025', 10),
          ignoreTLS: true,
        },
        defaults: {
          from: '"SENTINEL Platform" <sentinel@sentinel.local>',
        },
      }),
    }),
  ],
  controllers: [RegistrationController],
  providers: [
    JwtStrategy,
    JwtAuthGuard,
    AuthAuditService,
    KeycloakAdminService,
    {
      provide: APP_GUARD,
      useClass: ThrottlerGuard,
    },
    {
      provide: APP_INTERCEPTOR,
      useClass: AuthPropagationInterceptor,
    },
  ],
  exports: [PassportModule, JwtAuthGuard, AuthAuditService],
})
export class AuthModule {}
```

- [ ] **Step 2: Run existing auth module tests to verify nothing broke**

```bash
npx nx test api-gateway --testFile=src/modules/auth/jwt-auth.guard.spec.ts
```

Expected: All existing tests PASS.

- [ ] **Step 3: Commit**

```bash
git add apps/api-gateway/src/modules/auth/auth.module.ts
git commit -m "feat(auth): register KeycloakAdminService, RegistrationController, ThrottlerModule, MailerModule in AuthModule"
```

---

## Chunk 2: Angular Frontend — Registration & Admin

### Task 6: Update `app.component.ts` — rename `isLoginRoute` to `isFullScreenRoute`

**Files:**
- Modify: `apps/web/src/app/app.component.ts:21,205,224,229`

- [ ] **Step 1: Rename signal declaration (line 205)**

In `apps/web/src/app/app.component.ts`, change:

```typescript
  isLoginRoute = signal<boolean>(false);
```

to:

```typescript
  isFullScreenRoute = signal<boolean>(false);
```

- [ ] **Step 2: Update template binding (line 21)**

Change:

```typescript
    @if (isLoginRoute()) {
```

to:

```typescript
    @if (isFullScreenRoute()) {
```

- [ ] **Step 3: Update both `.set()` calls in `ngOnInit` (lines 224, 229)**

Change line 224:

```typescript
    this.isLoginRoute.set(this.router.url.replace(/\?.*$/, '') === '/login');
```

to:

```typescript
    this.isFullScreenRoute.set(['/login', '/register'].includes(this.router.url.replace(/\?.*$/, '')));
```

Change line 229:

```typescript
      this.isLoginRoute.set(e.urlAfterRedirects.replace(/\?.*$/, '') === '/login');
```

to:

```typescript
      this.isFullScreenRoute.set(['/login', '/register'].includes(e.urlAfterRedirects.replace(/\?.*$/, '')));
```

- [ ] **Step 4: Verify the app compiles**

```bash
npx nx build web --skip-nx-cache 2>&1 | tail -5
```

Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/app.component.ts
git commit -m "refactor: rename isLoginRoute to isFullScreenRoute, add /register exclusion"
```

---

### Task 7: Update `auth.service.ts` dev profile and add `/register`, `/admin/users` routes

**Files:**
- Modify: `apps/web/src/app/core/services/auth.service.ts:309`
- Modify: `apps/web/src/app/app.routes.ts`

- [ ] **Step 1: Add `sentinel-admin` to dev profile roles**

In `apps/web/src/app/core/services/auth.service.ts`, change line 309:

```typescript
      roles: ['sentinel-analyst', 'sentinel-operator'],
```

to:

```typescript
      roles: ['sentinel-analyst', 'sentinel-operator', 'sentinel-admin'],
```

- [ ] **Step 2: Add routes to `app.routes.ts`**

In `apps/web/src/app/app.routes.ts`, add the register and admin routes. After the login route (line 56) and before the wildcard route (line 58), add:

```typescript
  {
    path: 'register',
    loadComponent: () =>
      import('./features/register/register.component').then((m) => m.RegisterComponent),
  },
  {
    path: 'admin/users',
    canActivate: [roleGuard('sentinel-admin')],
    loadComponent: () =>
      import('./features/admin/pending-users/pending-users.component').then(
        (m) => m.PendingUsersComponent,
      ),
  },
```

- [ ] **Step 3: Run auth service tests**

```bash
npx nx test web --testFile=src/app/core/services/auth.service.spec.ts
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add apps/web/src/app/core/services/auth.service.ts apps/web/src/app/app.routes.ts
git commit -m "feat(auth): add sentinel-admin to dev profile, add /register and /admin/users routes"
```

---

### Task 8: Create `RegisterComponent`

**Files:**
- Create: `apps/web/src/app/features/register/register.component.ts`
- Create: `apps/web/src/app/features/register/register.component.spec.ts`

- [ ] **Step 1: Write the failing tests**

Create `apps/web/src/app/features/register/register.component.spec.ts`:

```typescript
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Router } from '@angular/router';
import { RegisterComponent } from './register.component';

describe('RegisterComponent', () => {
  let component: RegisterComponent;
  let fixture: ComponentFixture<RegisterComponent>;
  let httpTesting: HttpTestingController;
  let router: { navigate: jest.Mock };

  beforeEach(async () => {
    router = { navigate: jest.fn() };

    await TestBed.configureTestingModule({
      imports: [RegisterComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: Router, useValue: router },
      ],
    }).compileComponents();

    httpTesting = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(RegisterComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  afterEach(() => {
    httpTesting.verify();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should render all form fields', () => {
    const inputs = fixture.nativeElement.querySelectorAll('input, textarea');
    // username, email, password, confirmPassword, firstName, lastName, organization = 7 inputs
    // justification = 1 textarea
    expect(inputs.length).toBe(8);
  });

  it('should render SENTINEL title', () => {
    const title = fixture.nativeElement.querySelector('.app-title');
    expect(title.textContent).toContain('SENTINEL');
  });

  it('should disable submit when required fields are empty', () => {
    const btn = fixture.nativeElement.querySelector('.register-btn');
    expect(btn.disabled).toBe(true);
  });

  it('should show password mismatch error', async () => {
    component.form.username = 'test';
    component.form.email = 'test@example.com';
    component.form.password = 'Password1!';
    component.form.confirmPassword = 'Different1!';
    component.form.firstName = 'Test';
    component.form.lastName = 'User';
    component.form.organization = 'Org';
    component.form.justification = 'Reason';

    await component.onSubmit();

    expect(component.errorMessage()).toBe('Passwords do not match');
  });

  it('should show password too short error', async () => {
    component.form.username = 'test';
    component.form.email = 'test@example.com';
    component.form.password = 'short';
    component.form.confirmPassword = 'short';
    component.form.firstName = 'Test';
    component.form.lastName = 'User';
    component.form.organization = 'Org';
    component.form.justification = 'Reason';

    await component.onSubmit();

    expect(component.errorMessage()).toBe('Password must be at least 8 characters');
  });

  it('should submit registration and show success', () => {
    component.form.username = 'newuser';
    component.form.email = 'new@test.com';
    component.form.password = 'Password1!';
    component.form.confirmPassword = 'Password1!';
    component.form.firstName = 'New';
    component.form.lastName = 'User';
    component.form.organization = 'Test Org';
    component.form.justification = 'Need access';

    component.onSubmit();

    const req = httpTesting.expectOne('/api/auth/register');
    expect(req.request.method).toBe('POST');
    expect(req.request.body.username).toBe('newuser');
    req.flush({ message: 'Success' });

    expect(component.submitted()).toBe(true);
  });

  it('should show server error on failed registration', () => {
    component.form.username = 'existing';
    component.form.email = 'existing@test.com';
    component.form.password = 'Password1!';
    component.form.confirmPassword = 'Password1!';
    component.form.firstName = 'E';
    component.form.lastName = 'X';
    component.form.organization = 'Org';
    component.form.justification = 'Reason';

    component.onSubmit();

    const req = httpTesting.expectOne('/api/auth/register');
    req.flush({ message: 'User exists' }, { status: 409, statusText: 'Conflict' });

    expect(component.errorMessage()).toBeTruthy();
    expect(component.submitted()).toBe(false);
  });

  it('should have a link back to login', () => {
    const link = fixture.nativeElement.querySelector('a[href]');
    expect(link).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
npx nx test web --testFile=src/app/features/register/register.component.spec.ts
```

Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

Create `apps/web/src/app/features/register/register.component.ts`:

```typescript
import { Component, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { HttpClient } from '@angular/common/http';

@Component({
  selector: 'app-register',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [FormsModule, RouterLink],
  template: `
    <div class="login-page">
      <!-- Animated background (same as login) -->
      <div class="bg-grid"></div>
      <div class="radar-container">
        <div class="radar-circle radar-circle-1"></div>
        <div class="radar-circle radar-circle-2"></div>
        <div class="radar-circle radar-circle-3"></div>
        <div class="radar-sweep"></div>
      </div>

      <!-- Registration card -->
      <div class="login-card register-card">
        <div class="logo-mark">
          <div class="crosshair">
            <div class="crosshair-dot"></div>
          </div>
        </div>
        <h1 class="app-title">SENTINEL</h1>
        <p class="app-subtitle">Request Access</p>
        <div class="divider"></div>

        @if (submitted()) {
          <div class="success-message">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#22c55e" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10"/>
              <path d="M9 12l2 2 4-4"/>
            </svg>
            <p class="success-title">REQUEST SUBMITTED</p>
            <p class="success-text">Your access request has been submitted for review. You will receive an email when your account is approved.</p>
            <a class="back-link" routerLink="/login">Return to Sign In</a>
          </div>
        } @else {
          <form class="login-form" (ngSubmit)="onSubmit()">
            <div class="form-row">
              <div class="input-group">
                <label class="input-label" for="firstName">FIRST NAME</label>
                <input id="firstName" class="login-input" type="text"
                  [(ngModel)]="form.firstName" name="firstName"
                  placeholder="First name" [disabled]="loading()" />
              </div>
              <div class="input-group">
                <label class="input-label" for="lastName">LAST NAME</label>
                <input id="lastName" class="login-input" type="text"
                  [(ngModel)]="form.lastName" name="lastName"
                  placeholder="Last name" [disabled]="loading()" />
              </div>
            </div>
            <div class="input-group">
              <label class="input-label" for="username">USERNAME</label>
              <input id="username" class="login-input" type="text"
                [(ngModel)]="form.username" name="username"
                autocomplete="username" placeholder="Choose a username" [disabled]="loading()" />
            </div>
            <div class="input-group">
              <label class="input-label" for="email">EMAIL</label>
              <input id="email" class="login-input" type="email"
                [(ngModel)]="form.email" name="email"
                autocomplete="email" placeholder="Email address" [disabled]="loading()" />
            </div>
            <div class="form-row">
              <div class="input-group">
                <label class="input-label" for="password">PASSWORD</label>
                <input id="password" class="login-input" type="password"
                  [(ngModel)]="form.password" name="password"
                  autocomplete="new-password" placeholder="Min 8 characters" [disabled]="loading()" />
              </div>
              <div class="input-group">
                <label class="input-label" for="confirmPassword">CONFIRM PASSWORD</label>
                <input id="confirmPassword" class="login-input" type="password"
                  [(ngModel)]="form.confirmPassword" name="confirmPassword"
                  autocomplete="new-password" placeholder="Confirm password" [disabled]="loading()" />
              </div>
            </div>
            <div class="input-group">
              <label class="input-label" for="organization">ORGANIZATION / UNIT</label>
              <input id="organization" class="login-input" type="text"
                [(ngModel)]="form.organization" name="organization"
                placeholder="Your organization or unit" [disabled]="loading()" />
            </div>
            <div class="input-group">
              <label class="input-label" for="justification">JUSTIFICATION FOR ACCESS</label>
              <textarea id="justification" class="login-input textarea"
                [(ngModel)]="form.justification" name="justification"
                placeholder="Explain why you need access to SENTINEL"
                rows="3" [disabled]="loading()"></textarea>
            </div>
            @if (errorMessage()) {
              <p class="error-message">{{ errorMessage() }}</p>
            }
            <button class="register-btn" type="submit"
              [disabled]="loading() || !isFormValid()">
              @if (loading()) {
                SUBMITTING...
              } @else {
                REQUEST ACCESS
              }
            </button>
          </form>
          <p class="auth-notice">
            Already have an account? <a class="login-link" routerLink="/login">Sign in</a>
          </p>
        }
      </div>
    </div>
  `,
  styles: [`
    :host { display: block; width: 100vw; height: 100vh; overflow: hidden; }

    .login-page {
      position: relative; width: 100%; height: 100%;
      background: linear-gradient(135deg, #060e1f 0%, #0e1e3d 50%, #091428 100%);
      display: flex; align-items: center; justify-content: center;
    }

    .bg-grid {
      position: absolute; inset: 0; pointer-events: none;
      background-image:
        linear-gradient(rgba(59, 130, 246, 0.07) 1px, transparent 1px),
        linear-gradient(90deg, rgba(59, 130, 246, 0.07) 1px, transparent 1px);
      background-size: 60px 60px;
    }

    .radar-container { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); pointer-events: none; }
    .radar-circle { position: absolute; border-radius: 50%; border: 1px solid rgba(59, 130, 246, 0.1); top: 50%; left: 50%; transform: translate(-50%, -50%); }
    .radar-circle-1 { width: 500px; height: 500px; }
    .radar-circle-2 { width: 350px; height: 350px; border-color: rgba(59, 130, 246, 0.15); }
    .radar-circle-3 { width: 200px; height: 200px; }
    .radar-sweep {
      position: absolute; top: 50%; left: 50%; width: 250px; height: 250px; transform-origin: 0 0;
      background: conic-gradient(from 0deg, transparent 0deg, rgba(59, 130, 246, 0.08) 30deg, transparent 60deg);
      animation: sweep 8s linear infinite;
    }
    @keyframes sweep { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

    .login-card {
      position: relative; z-index: 10; width: 340px; padding: 40px 36px;
      background: rgba(8, 16, 38, 0.92);
      border: 1px solid rgba(59, 130, 246, 0.2); border-radius: 12px; text-align: center;
      box-shadow: 0 0 60px rgba(59, 130, 246, 0.08), 0 25px 80px rgba(0, 0, 0, 0.5);
      backdrop-filter: blur(12px);
      animation: cardAppear 0.6s ease-out;
      max-height: 90vh; overflow-y: auto;
    }

    .register-card { width: 440px; }

    @keyframes cardAppear {
      from { opacity: 0; transform: translateY(20px) scale(0.97); }
      to { opacity: 1; transform: translateY(0) scale(1); }
    }

    .logo-mark {
      width: 52px; height: 52px; margin: 0 auto 18px; border-radius: 10px;
      background: linear-gradient(135deg, #3b82f6, #2563eb);
      display: flex; align-items: center; justify-content: center;
      box-shadow: 0 0 24px rgba(59, 130, 246, 0.3);
    }
    .crosshair { width: 22px; height: 22px; border: 2.5px solid rgba(255,255,255,0.9); border-radius: 50%; position: relative; }
    .crosshair-dot { position: absolute; top: 50%; left: 50%; transform: translate(-50%,-50%); width: 5px; height: 5px; background: white; border-radius: 50%; }

    .app-title { font-family: 'Inter', system-ui, sans-serif; font-size: 28px; font-weight: 700; letter-spacing: 6px; color: rgba(255,255,255,0.95); margin: 0; }
    .app-subtitle { font-family: 'Inter', system-ui, sans-serif; font-size: 12px; color: rgba(255,255,255,0.4); margin: 6px 0 0; letter-spacing: 1.5px; }
    .divider { width: 100%; height: 1px; background: linear-gradient(90deg, transparent, rgba(59,130,246,0.3), transparent); margin: 28px 0; }

    .login-form { display: flex; flex-direction: column; gap: 14px; }
    .form-row { display: flex; gap: 12px; }
    .form-row .input-group { flex: 1; }
    .input-group { text-align: left; }
    .input-label { display: block; font-family: 'Inter', system-ui, sans-serif; font-size: 10px; font-weight: 600; letter-spacing: 1.5px; color: rgba(255,255,255,0.4); margin-bottom: 6px; }

    .login-input {
      width: 100%; padding: 10px 12px;
      background: rgba(255,255,255,0.05); border: 1px solid rgba(59,130,246,0.2); border-radius: 8px;
      color: rgba(255,255,255,0.9); font-family: 'Inter', system-ui, sans-serif; font-size: 13px;
      outline: none; transition: border-color 0.2s, background 0.2s; box-sizing: border-box;
    }
    .login-input::placeholder { color: rgba(255,255,255,0.2); }
    .login-input:focus { border-color: rgba(59,130,246,0.5); background: rgba(255,255,255,0.08); }
    .login-input:disabled { opacity: 0.5; cursor: not-allowed; }
    .textarea { resize: vertical; min-height: 60px; font-family: 'Inter', system-ui, sans-serif; }

    .error-message { font-family: 'Inter', system-ui, sans-serif; font-size: 12px; color: #f87171; margin: 0; text-align: center; }

    .register-btn {
      width: 100%; padding: 13px 24px;
      background: linear-gradient(135deg, #3b82f6, #2563eb); color: white; border: none; border-radius: 8px;
      font-family: 'Inter', system-ui, sans-serif; font-size: 14px; font-weight: 600; letter-spacing: 2px;
      cursor: pointer; box-shadow: 0 4px 20px rgba(59,130,246,0.3);
      transition: box-shadow 0.2s, transform 0.15s;
    }
    .register-btn:hover:not(:disabled) { box-shadow: 0 6px 28px rgba(59,130,246,0.45); transform: translateY(-1px); }
    .register-btn:active:not(:disabled) { transform: translateY(0); box-shadow: 0 2px 12px rgba(59,130,246,0.3); }
    .register-btn:disabled { opacity: 0.6; cursor: not-allowed; }

    .auth-notice { font-family: 'Inter', system-ui, sans-serif; font-size: 11px; color: rgba(255,255,255,0.25); margin: 16px 0 0; letter-spacing: 0.5px; }
    .login-link { color: rgba(59,130,246,0.8); text-decoration: none; }
    .login-link:hover { color: rgba(59,130,246,1); text-decoration: underline; }

    .success-message { padding: 16px 0; display: flex; flex-direction: column; align-items: center; gap: 12px; }
    .success-title { font-family: 'Inter', system-ui, sans-serif; font-size: 16px; font-weight: 600; letter-spacing: 2px; color: #22c55e; margin: 0; }
    .success-text { font-family: 'Inter', system-ui, sans-serif; font-size: 13px; color: rgba(255,255,255,0.5); margin: 0; line-height: 1.5; }
    .back-link { font-family: 'Inter', system-ui, sans-serif; font-size: 13px; color: rgba(59,130,246,0.8); text-decoration: none; margin-top: 8px; }
    .back-link:hover { color: rgba(59,130,246,1); text-decoration: underline; }
  `],
})
export class RegisterComponent {
  private readonly http = inject(HttpClient);

  form = {
    username: '',
    email: '',
    password: '',
    confirmPassword: '',
    firstName: '',
    lastName: '',
    organization: '',
    justification: '',
  };

  loading = signal(false);
  errorMessage = signal('');
  submitted = signal(false);

  isFormValid(): boolean {
    return !!(
      this.form.username &&
      this.form.email &&
      this.form.password &&
      this.form.confirmPassword &&
      this.form.firstName &&
      this.form.lastName &&
      this.form.organization &&
      this.form.justification
    );
  }

  async onSubmit(): Promise<void> {
    if (!this.isFormValid()) return;

    if (this.form.password !== this.form.confirmPassword) {
      this.errorMessage.set('Passwords do not match');
      return;
    }

    if (this.form.password.length < 8) {
      this.errorMessage.set('Password must be at least 8 characters');
      return;
    }

    this.loading.set(true);
    this.errorMessage.set('');

    this.http.post<{ message: string }>('/api/auth/register', this.form).subscribe({
      next: () => {
        this.loading.set(false);
        this.submitted.set(true);
      },
      error: (err) => {
        this.loading.set(false);
        this.errorMessage.set(err.error?.message ?? 'Registration failed. Please try again.');
      },
    });
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
npx nx test web --testFile=src/app/features/register/register.component.spec.ts
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/register/register.component.ts apps/web/src/app/features/register/register.component.spec.ts
git commit -m "feat(auth): add RegisterComponent with form validation and branded UI"
```

---

### Task 9: Add "Request Access" link to `LoginComponent`

**Files:**
- Modify: `apps/web/src/app/features/login/login.component.ts:3,10,77`

- [ ] **Step 1: Add RouterLink import**

In `apps/web/src/app/features/login/login.component.ts`, change line 3:

```typescript
import { FormsModule } from '@angular/forms';
```

to:

```typescript
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
```

Update the imports array (line 10):

```typescript
  imports: [FormsModule],
```

to:

```typescript
  imports: [FormsModule, RouterLink],
```

- [ ] **Step 2: Replace the "Authorized personnel only" notice**

Change line 77:

```html
        <p class="auth-notice">Authorized personnel only</p>
```

to:

```html
        <p class="auth-notice">
          Need access? <a class="request-link" routerLink="/register">Request an account</a>
        </p>
```

- [ ] **Step 3: Add CSS for the request link**

After the `.auth-notice` style block (around line 425), add:

```css
    .request-link {
      color: rgba(59, 130, 246, 0.8);
      text-decoration: none;
    }

    .request-link:hover {
      color: rgba(59, 130, 246, 1);
      text-decoration: underline;
    }
```

- [ ] **Step 4: Update the login component test**

Adding `RouterLink` to the component requires the test environment to have a router. In `apps/web/src/app/features/login/login.component.spec.ts`, add `provideRouter` to the imports and providers:

At the top of the file, add:
```typescript
import { provideRouter } from '@angular/router';
```

In the `providers` array inside `TestBed.configureTestingModule`, add `provideRouter([])`:
```typescript
providers: [
  { provide: AuthService, useValue: authService },
  { provide: Router, useValue: router },
  {
    provide: ActivatedRoute,
    useValue: { snapshot: { queryParams } },
  },
  provideRouter([]),
],
```

Run:

```bash
npx nx test web --testFile=src/app/features/login/login.component.spec.ts
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/login/login.component.ts
git commit -m "feat(auth): add 'Request an account' link to login page"
```

---

### Task 10: Create `PendingUsersComponent`

**Files:**
- Create: `apps/web/src/app/features/admin/pending-users/pending-users.component.ts`
- Create: `apps/web/src/app/features/admin/pending-users/pending-users.component.spec.ts`

- [ ] **Step 1: Write the failing tests**

Create `apps/web/src/app/features/admin/pending-users/pending-users.component.spec.ts`:

```typescript
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { PendingUsersComponent } from './pending-users.component';

describe('PendingUsersComponent', () => {
  let component: PendingUsersComponent;
  let fixture: ComponentFixture<PendingUsersComponent>;
  let httpTesting: HttpTestingController;

  const mockPendingUsers = [
    {
      id: 'u1',
      username: 'pending1',
      email: 'p1@test.com',
      firstName: 'Pending',
      lastName: 'One',
      organization: 'TestOrg',
      justification: 'Need access',
      registrationDate: '2026-03-11T10:00:00Z',
    },
    {
      id: 'u2',
      username: 'pending2',
      email: 'p2@test.com',
      firstName: 'Pending',
      lastName: 'Two',
      organization: 'OtherOrg',
      justification: 'Analyst work',
      registrationDate: '2026-03-10T08:00:00Z',
    },
  ];

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [PendingUsersComponent],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();

    httpTesting = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(PendingUsersComponent);
    component = fixture.componentInstance;
  });

  afterEach(() => {
    httpTesting.verify();
  });

  it('should create and fetch pending users on init', () => {
    fixture.detectChanges();

    const req = httpTesting.expectOne('/api/auth/pending-registrations');
    expect(req.request.method).toBe('GET');
    req.flush(mockPendingUsers);

    expect(component.users().length).toBe(2);
  });

  it('should render a table with pending users', () => {
    fixture.detectChanges();
    httpTesting.expectOne('/api/auth/pending-registrations').flush(mockPendingUsers);
    fixture.detectChanges();

    const rows = fixture.nativeElement.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);
  });

  it('should call approve endpoint and remove user from list', () => {
    fixture.detectChanges();
    httpTesting.expectOne('/api/auth/pending-registrations').flush(mockPendingUsers);
    fixture.detectChanges();

    component.approve('u1');

    const req = httpTesting.expectOne('/api/auth/approve-registration/u1');
    expect(req.request.method).toBe('POST');
    req.flush({ message: 'approved' });

    expect(component.users().length).toBe(1);
    expect(component.users()[0].id).toBe('u2');
  });

  it('should call reject endpoint and remove user from list', () => {
    fixture.detectChanges();
    httpTesting.expectOne('/api/auth/pending-registrations').flush(mockPendingUsers);
    fixture.detectChanges();

    component.reject('u2');

    const req = httpTesting.expectOne('/api/auth/reject-registration/u2');
    expect(req.request.method).toBe('POST');
    req.flush({ message: 'rejected' });

    expect(component.users().length).toBe(1);
    expect(component.users()[0].id).toBe('u1');
  });

  it('should show error on failed approval', () => {
    fixture.detectChanges();
    httpTesting.expectOne('/api/auth/pending-registrations').flush(mockPendingUsers);
    fixture.detectChanges();

    component.approve('u1');

    httpTesting.expectOne('/api/auth/approve-registration/u1').flush(
      { message: 'Failed' },
      { status: 500, statusText: 'Error' },
    );

    expect(component.errorMessage()).toBeTruthy();
    expect(component.users().length).toBe(2); // user not removed
  });

  it('should show empty state when no pending users', () => {
    fixture.detectChanges();
    httpTesting.expectOne('/api/auth/pending-registrations').flush([]);
    fixture.detectChanges();

    const empty = fixture.nativeElement.querySelector('.empty-state');
    expect(empty).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
npx nx test web --testFile=src/app/features/admin/pending-users/pending-users.component.spec.ts
```

Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

Create `apps/web/src/app/features/admin/pending-users/pending-users.component.ts`:

```typescript
import { Component, OnInit, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { CommonModule } from '@angular/common';
import { HttpClient } from '@angular/common/http';

interface PendingUser {
  id: string;
  username: string;
  email: string;
  firstName: string;
  lastName: string;
  organization: string;
  justification: string;
  registrationDate: string;
}

@Component({
  selector: 'app-pending-users',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule],
  template: `
    <div class="panel">
      <div class="panel-header">
        <h2 class="panel-title">Pending Registrations</h2>
        <button class="refresh-btn" (click)="loadUsers()" [disabled]="loadingList()">
          Refresh
        </button>
      </div>

      @if (errorMessage()) {
        <div class="toast error-toast">{{ errorMessage() }}</div>
      }
      @if (successMessage()) {
        <div class="toast success-toast">{{ successMessage() }}</div>
      }

      @if (loadingList()) {
        <div class="loading-state">Loading pending registrations...</div>
      } @else if (users().length === 0) {
        <div class="empty-state">No pending registration requests.</div>
      } @else {
        <div class="table-container">
          <table class="users-table">
            <thead>
              <tr>
                <th>Username</th>
                <th>Email</th>
                <th>Name</th>
                <th>Organization</th>
                <th>Justification</th>
                <th>Requested</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              @for (user of users(); track user.id) {
                <tr>
                  <td class="font-mono">{{ user.username }}</td>
                  <td>{{ user.email }}</td>
                  <td>{{ user.firstName }} {{ user.lastName }}</td>
                  <td>{{ user.organization }}</td>
                  <td class="justification-cell">{{ user.justification }}</td>
                  <td class="font-mono">{{ user.registrationDate | date:'short' }}</td>
                  <td class="actions-cell">
                    <button class="action-btn approve-btn"
                      (click)="approve(user.id)"
                      [disabled]="actionInProgress() === user.id">
                      @if (actionInProgress() === user.id && actionType() === 'approve') {
                        ...
                      } @else {
                        Approve
                      }
                    </button>
                    <button class="action-btn reject-btn"
                      (click)="reject(user.id)"
                      [disabled]="actionInProgress() === user.id">
                      @if (actionInProgress() === user.id && actionType() === 'reject') {
                        ...
                      } @else {
                        Reject
                      }
                    </button>
                  </td>
                </tr>
              }
            </tbody>
          </table>
        </div>
      }
    </div>
  `,
  styles: [`
    :host { display: block; pointer-events: all; }

    .panel {
      width: 800px; max-width: calc(100vw - 120px); max-height: calc(100vh - 80px);
      background: var(--panel-bg, rgba(15, 23, 42, 0.97));
      border: 1px solid var(--border-color, rgba(59, 130, 246, 0.15));
      border-radius: 12px; padding: 24px;
      box-shadow: 0 25px 80px rgba(0, 0, 0, 0.5);
      overflow-y: auto; margin: 20px;
    }

    .panel-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
    .panel-title { font-family: 'Inter', system-ui, sans-serif; font-size: 18px; font-weight: 600; letter-spacing: 1px; color: rgba(255,255,255,0.9); margin: 0; }
    .refresh-btn {
      padding: 6px 16px; background: rgba(255,255,255,0.05); border: 1px solid rgba(59,130,246,0.2);
      border-radius: 6px; color: rgba(255,255,255,0.7); font-size: 12px; cursor: pointer;
      transition: background 0.2s;
    }
    .refresh-btn:hover:not(:disabled) { background: rgba(255,255,255,0.1); }
    .refresh-btn:disabled { opacity: 0.5; cursor: not-allowed; }

    .toast { padding: 10px 16px; border-radius: 6px; font-size: 13px; margin-bottom: 16px; }
    .error-toast { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.3); color: #f87171; }
    .success-toast { background: rgba(34,197,94,0.1); border: 1px solid rgba(34,197,94,0.3); color: #22c55e; }

    .loading-state, .empty-state { text-align: center; padding: 40px; color: rgba(255,255,255,0.4); font-size: 14px; }

    .table-container { overflow-x: auto; }
    .users-table { width: 100%; border-collapse: collapse; font-size: 13px; }
    .users-table th {
      text-align: left; padding: 10px 12px; font-family: 'Inter', system-ui, sans-serif;
      font-size: 10px; font-weight: 600; letter-spacing: 1px;
      color: rgba(255,255,255,0.4); border-bottom: 1px solid rgba(59,130,246,0.15);
    }
    .users-table td {
      padding: 10px 12px; color: rgba(255,255,255,0.8);
      border-bottom: 1px solid rgba(255,255,255,0.04);
    }
    .users-table tbody tr:hover { background: rgba(59,130,246,0.04); }
    .font-mono { font-family: 'JetBrains Mono', monospace; }
    .justification-cell { max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

    .actions-cell { white-space: nowrap; display: flex; gap: 6px; }
    .action-btn {
      padding: 5px 12px; border: none; border-radius: 4px; font-size: 11px;
      font-weight: 600; cursor: pointer; transition: opacity 0.2s;
    }
    .action-btn:disabled { opacity: 0.5; cursor: not-allowed; }
    .approve-btn { background: rgba(34,197,94,0.15); color: #22c55e; }
    .approve-btn:hover:not(:disabled) { background: rgba(34,197,94,0.25); }
    .reject-btn { background: rgba(248,113,113,0.15); color: #f87171; }
    .reject-btn:hover:not(:disabled) { background: rgba(248,113,113,0.25); }
  `],
})
export class PendingUsersComponent implements OnInit {
  private readonly http = inject(HttpClient);

  users = signal<PendingUser[]>([]);
  loadingList = signal(false);
  actionInProgress = signal<string | null>(null);
  actionType = signal<'approve' | 'reject' | null>(null);
  errorMessage = signal('');
  successMessage = signal('');

  ngOnInit(): void {
    this.loadUsers();
  }

  loadUsers(): void {
    this.loadingList.set(true);
    this.errorMessage.set('');

    this.http.get<PendingUser[]>('/api/auth/pending-registrations').subscribe({
      next: (users) => {
        this.users.set(users);
        this.loadingList.set(false);
      },
      error: () => {
        this.errorMessage.set('Failed to load pending registrations');
        this.loadingList.set(false);
      },
    });
  }

  approve(userId: string): void {
    this.actionInProgress.set(userId);
    this.actionType.set('approve');
    this.clearMessages();

    this.http.post<{ message: string }>(`/api/auth/approve-registration/${userId}`, {}).subscribe({
      next: () => {
        this.users.set(this.users().filter((u) => u.id !== userId));
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.successMessage.set('User approved successfully');
        this.autoClearSuccess();
      },
      error: (err) => {
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.errorMessage.set(err.error?.message ?? 'Failed to approve user');
      },
    });
  }

  reject(userId: string): void {
    this.actionInProgress.set(userId);
    this.actionType.set('reject');
    this.clearMessages();

    this.http.post<{ message: string }>(`/api/auth/reject-registration/${userId}`, {}).subscribe({
      next: () => {
        this.users.set(this.users().filter((u) => u.id !== userId));
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.successMessage.set('User rejected and notification sent');
        this.autoClearSuccess();
      },
      error: (err) => {
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.errorMessage.set(err.error?.message ?? 'Failed to reject user');
      },
    });
  }

  private clearMessages(): void {
    this.errorMessage.set('');
    this.successMessage.set('');
  }

  private autoClearSuccess(): void {
    setTimeout(() => this.successMessage.set(''), 3000);
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
npx nx test web --testFile=src/app/features/admin/pending-users/pending-users.component.spec.ts
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/admin/pending-users/pending-users.component.ts apps/web/src/app/features/admin/pending-users/pending-users.component.spec.ts
git commit -m "feat(auth): add PendingUsersComponent for admin registration approval"
```

---

## Chunk 3: Integration Verification

### Task 11: Run all tests and verify build

**Files:** None (verification only)

- [ ] **Step 1: Run all api-gateway auth tests**

```bash
npx nx test api-gateway
```

Expected: All tests PASS.

- [ ] **Step 2: Run all web tests**

```bash
npx nx test web
```

Expected: All tests PASS.

- [ ] **Step 3: Verify the web app builds**

```bash
npx nx build web --skip-nx-cache
```

Expected: Build succeeds within bundle budget.

- [ ] **Step 4: Verify the api-gateway builds**

```bash
npx nx build api-gateway --skip-nx-cache
```

Expected: Build succeeds.

- [ ] **Step 5: Final commit (if any test/build fixes were needed)**

Only if fixes were made:

```bash
git add -A
git commit -m "fix(auth): resolve test/build issues from registration workflow"
```
