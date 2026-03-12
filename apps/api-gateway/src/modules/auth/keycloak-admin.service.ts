import { Injectable, Logger, HttpException, HttpStatus } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';

export const VALID_CLASSIFICATIONS = ['classification-u', 'classification-s', 'classification-ts'] as const;
export type ClassificationLevel = typeof VALID_CLASSIFICATIONS[number];

export interface ActiveUser {
  id: string;
  username: string;
  email: string;
  firstName: string;
  lastName: string;
  classificationLevel: ClassificationLevel | null;
  roles: string[];
}

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

interface TokenCache {
  token: string;
  expiresAt: number;
}

@Injectable()
export class KeycloakAdminService {
  private readonly logger = new Logger(KeycloakAdminService.name);
  private tokenCache: TokenCache | null = null;

  constructor(private readonly configService: ConfigService) {}

  private get baseUrl(): string {
    return this.configService.get<string>('KEYCLOAK_URL', 'http://localhost:8080');
  }

  private get realm(): string {
    return this.configService.get<string>('KEYCLOAK_REALM', 'sentinel');
  }

  private get clientId(): string {
    return this.configService.get<string>(
      'KEYCLOAK_SERVICE_CLIENT_ID',
      'sentinel-service',
    );
  }

  private get clientSecret(): string {
    return this.configService.get<string>(
      'KEYCLOAK_SERVICE_CLIENT_SECRET',
      'sentinel-service-secret-dev',
    );
  }

  /**
   * Obtains a service account token via client credentials grant.
   * Caches the token and refreshes it 30 seconds before expiry.
   */
  private async getToken(): Promise<string> {
    const now = Date.now();

    if (this.tokenCache && now < this.tokenCache.expiresAt - 30_000) {
      return this.tokenCache.token;
    }

    const tokenUrl = `${this.baseUrl}/realms/${this.realm}/protocol/openid-connect/token`;

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
      const text = await response.text();
      this.logger.error(`Failed to obtain admin token: ${response.status} ${text}`);
      throw new HttpException(
        'Failed to obtain admin token',
        HttpStatus.SERVICE_UNAVAILABLE,
      );
    }

    const data = (await response.json()) as { access_token: string; expires_in: number };

    this.tokenCache = {
      token: data.access_token,
      expiresAt: now + data.expires_in * 1000,
    };

    return this.tokenCache.token;
  }

  /**
   * Makes an authenticated request to the Keycloak Admin REST API.
   */
  private async adminRequest(
    path: string,
    options: RequestInit = {},
  ): Promise<Response> {
    const token = await this.getToken();
    const url = `${this.baseUrl}/admin/realms/${this.realm}${path}`;

    const headers: Record<string, string> = {
      Authorization: `Bearer ${token}`,
      ...(options.headers as Record<string, string> | undefined),
    };

    return fetch(url, { ...options, headers });
  }

  /**
   * Creates a new disabled user in Keycloak with registration attributes.
   * The user must be approved before they can log in.
   */
  async createUser(dto: CreateUserDto): Promise<void> {
    if (dto.password.length < 8) {
      throw new HttpException(
        'Password must be at least 8 characters',
        HttpStatus.BAD_REQUEST,
      );
    }

    const userRepresentation = {
      username: dto.username,
      email: dto.email,
      firstName: dto.firstName,
      lastName: dto.lastName,
      enabled: false,
      credentials: [
        {
          type: 'password',
          value: dto.password,
          temporary: false,
        },
      ],
      attributes: {
        organization: [dto.organization],
        justification: [dto.justification],
        registrationDate: [new Date().toISOString()],
      },
    };

    const response = await this.adminRequest('/users', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(userRepresentation),
    });

    if (response.status === 409) {
      let detail = 'Username or email already exists';
      try {
        const body = (await response.json()) as { errorMessage?: string };
        if (body.errorMessage) {
          detail = body.errorMessage;
        }
      } catch {
        // ignore parse errors — fall back to default message
      }
      throw new HttpException(detail, HttpStatus.CONFLICT);
    }

    if (!response.ok) {
      const text = await response.text();
      this.logger.error(`Failed to create user: ${response.status} ${text}`);
      throw new HttpException('Failed to create user', HttpStatus.INTERNAL_SERVER_ERROR);
    }
  }

  /**
   * Returns a list of users pending approval (disabled users with a registrationDate attribute).
   */
  async getPendingRegistrations(): Promise<PendingUser[]> {
    const response = await this.adminRequest('/users?enabled=false&max=100');

    if (!response.ok) {
      const text = await response.text();
      this.logger.error(`Failed to fetch pending registrations: ${response.status} ${text}`);
      throw new HttpException(
        'Failed to fetch pending registrations',
        HttpStatus.INTERNAL_SERVER_ERROR,
      );
    }

    const users = (await response.json()) as Array<{
      id: string;
      username: string;
      email: string;
      firstName: string;
      lastName: string;
      attributes?: Record<string, string[]>;
    }>;

    return users
      .filter((u) => u.attributes?.registrationDate)
      .map((u) => ({
        id: u.id,
        username: u.username,
        email: u.email,
        firstName: u.firstName,
        lastName: u.lastName,
        organization: u.attributes?.organization?.[0] ?? '',
        justification: u.attributes?.justification?.[0] ?? '',
        registrationDate: u.attributes!.registrationDate![0],
      }));
  }

  /**
   * Approves a pending user registration:
   * 1. GET user to read current attributes
   * 2. PUT enable only — attributes (including registrationDate) are left intact so that
   *    a rollback leaves the user visible in getPendingRegistrations()
   * 3. Fetch realm roles and assign sentinel-viewer + classification-u
   *    — on failure: re-disable user and re-throw (registrationDate still present)
   * 4. PUT remove registrationDate attribute (only after roles succeed)
   * 5. POST /execute-actions-email to trigger the VERIFY_EMAIL email
   */
  async approveUser(userId: string, classificationLevel: ClassificationLevel = 'classification-u'): Promise<void> {
    // Step 1: Get current user representation
    const userResponse = await this.adminRequest(`/users/${userId}`);
    if (!userResponse.ok) {
      throw new HttpException('User not found', HttpStatus.NOT_FOUND);
    }

    const user = (await userResponse.json()) as {
      attributes?: Record<string, string[]>;
    };

    // Step 2: Enable user only — keep all attributes intact for safe rollback
    const enableResponse = await this.adminRequest(`/users/${userId}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: true }),
    });

    if (!enableResponse.ok) {
      const text = await enableResponse.text();
      this.logger.error(`Failed to enable user ${userId}: ${enableResponse.status} ${text}`);
      throw new HttpException('Failed to approve user', HttpStatus.INTERNAL_SERVER_ERROR);
    }

    // Step 3: Fetch available realm roles and assign sentinel-viewer + classification-u
    // If this fails, re-disable the user so registrationDate is preserved and they remain
    // visible in getPendingRegistrations().
    try {
      const rolesResponse = await this.adminRequest('/roles');
      if (!rolesResponse.ok) {
        throw new Error(`Failed to fetch roles: ${rolesResponse.status}`);
      }

      const allRoles = (await rolesResponse.json()) as Array<{ id: string; name: string }>;

      const rolesToAssign = allRoles.filter(
        (r) => r.name === 'sentinel-viewer' || r.name === classificationLevel,
      );

      if (rolesToAssign.length === 0) {
        throw new Error(`Required roles (sentinel-viewer, ${classificationLevel}) not found`);
      }

      const assignResponse = await this.adminRequest(
        `/users/${userId}/role-mappings/realm`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(rolesToAssign),
        },
      );

      if (!assignResponse.ok) {
        throw new Error(`Failed to assign roles: ${assignResponse.status}`);
      }
    } catch (error) {
      // Re-disable user — registrationDate attribute is still intact so the user
      // remains visible in getPendingRegistrations() and can be retried.
      this.logger.error(
        `Role assignment failed for user ${userId}, re-disabling: ${(error as Error).message}`,
      );

      await this.adminRequest(`/users/${userId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: false }),
      });

      throw new HttpException(
        'Failed to assign roles; user has been re-disabled',
        HttpStatus.INTERNAL_SERVER_ERROR,
      );
    }

    // Step 4: Remove registrationDate attribute now that roles are assigned
    const cleanAttributes = { ...(user.attributes ?? {}) };
    delete cleanAttributes['registrationDate'];

    const cleanupResponse = await this.adminRequest(`/users/${userId}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ attributes: cleanAttributes }),
    });

    if (!cleanupResponse.ok) {
      this.logger.error(
        `Failed to remove registrationDate for user ${userId}: ${cleanupResponse.status}`,
      );
      // Non-fatal: user is enabled and has roles; log and continue
    }

    // Step 5: Trigger the VERIFY_EMAIL action email via the execute-actions-email endpoint
    const emailResponse = await this.adminRequest(
      `/users/${userId}/execute-actions-email`,
      {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(['VERIFY_EMAIL']),
      },
    );

    if (!emailResponse.ok) {
      this.logger.error(
        `Failed to send verification email to user ${userId}: ${emailResponse.status}`,
      );
      // Non-fatal: user is enabled and has roles; log and continue
    }
  }

  /**
   * Rejects a pending user registration by deleting their account.
   * Returns the user's email and firstName for notification purposes.
   */
  async rejectUser(userId: string): Promise<{ email: string; firstName: string }> {
    // Get user info before deletion
    const userResponse = await this.adminRequest(`/users/${userId}`);
    if (!userResponse.ok) {
      throw new HttpException('User not found', HttpStatus.NOT_FOUND);
    }

    const user = (await userResponse.json()) as {
      email: string;
      firstName: string;
    };

    const deleteResponse = await this.adminRequest(`/users/${userId}`, {
      method: 'DELETE',
    });

    if (!deleteResponse.ok) {
      const text = await deleteResponse.text();
      this.logger.error(`Failed to delete user ${userId}: ${deleteResponse.status} ${text}`);
      throw new HttpException('Failed to reject user', HttpStatus.INTERNAL_SERVER_ERROR);
    }

    return { email: user.email, firstName: user.firstName };
  }

  /**
   * Returns all enabled (active) users with their classification level and roles.
   * Filters out service accounts (usernames starting with `service-account-`).
   */
  async getActiveUsers(): Promise<ActiveUser[]> {
    const response = await this.adminRequest('/users?enabled=true&max=100');
    if (!response.ok) {
      throw new HttpException('Failed to fetch users', HttpStatus.INTERNAL_SERVER_ERROR);
    }

    const users = (await response.json()) as Array<{
      id: string;
      username: string;
      email: string;
      firstName: string;
      lastName: string;
    }>;

    const activeUsers = users.filter(u => !u.username.startsWith('service-account-'));

    const results: ActiveUser[] = [];
    for (const u of activeUsers) {
      const rolesResponse = await this.adminRequest(`/users/${u.id}/role-mappings/realm`);
      const roles = rolesResponse.ok
        ? ((await rolesResponse.json()) as Array<{ name: string }>).map(r => r.name)
        : [];

      const classificationLevel = (VALID_CLASSIFICATIONS.find(c => roles.includes(c)) ?? null) as ClassificationLevel | null;

      results.push({
        id: u.id,
        username: u.username,
        email: u.email,
        firstName: u.firstName,
        lastName: u.lastName,
        classificationLevel,
        roles,
      });
    }

    return results;
  }
}
