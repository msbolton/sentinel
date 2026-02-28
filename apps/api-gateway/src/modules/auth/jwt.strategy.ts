import { Injectable, Logger, UnauthorizedException } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { PassportStrategy } from '@nestjs/passport';
import { Strategy, ExtractJwt } from 'passport-jwt';
import { passportJwtSecret } from 'jwks-rsa';

/**
 * Represents the decoded payload from a Keycloak-issued JWT.
 */
export interface KeycloakTokenPayload {
  /** Subject - the user's unique ID in Keycloak */
  sub: string;
  /** Preferred username */
  preferred_username: string;
  /** User's email address */
  email: string;
  /** Email verification status */
  email_verified: boolean;
  /** User's display name */
  name: string;
  /** Given name */
  given_name: string;
  /** Family name */
  family_name: string;
  /** Realm-level role assignments */
  realm_access: {
    roles: string[];
  };
  /** Client-level role assignments keyed by client ID */
  resource_access: Record<
    string,
    {
      roles: string[];
    }
  >;
  /** Token issuer URL */
  iss: string;
  /** Audience */
  aud: string | string[];
  /** Issued-at timestamp (epoch seconds) */
  iat: number;
  /** Expiration timestamp (epoch seconds) */
  exp: number;
  /** User's classification clearance level (custom claim) */
  classification_level?: string;
}

/**
 * Validated user object attached to the request after JWT verification.
 */
export interface AuthenticatedUser {
  userId: string;
  username: string;
  email: string;
  name: string;
  roles: string[];
  realmRoles: string[];
  clientRoles: string[];
  classificationLevel: string;
}

@Injectable()
export class JwtStrategy extends PassportStrategy(Strategy, 'jwt') {
  private readonly logger = new Logger(JwtStrategy.name);

  constructor(private readonly configService: ConfigService) {
    const keycloakBaseUrl = configService.get<string>(
      'KEYCLOAK_BASE_URL',
      'http://localhost:8080',
    );
    const keycloakRealm = configService.get<string>(
      'KEYCLOAK_REALM',
      'sentinel',
    );
    const issuerUrl = `${keycloakBaseUrl}/realms/${keycloakRealm}`;
    const jwksUri = `${issuerUrl}/protocol/openid-connect/certs`;

    super({
      jwtFromRequest: ExtractJwt.fromAuthHeaderAsBearerToken(),
      ignoreExpiration: false,
      audience: configService.get<string>('KEYCLOAK_AUDIENCE', 'sentinel-api'),
      issuer: issuerUrl,
      algorithms: ['RS256'],
      secretOrKeyProvider: passportJwtSecret({
        cache: true,
        rateLimit: true,
        jwksRequestsPerMinute: 10,
        jwksUri,
      }),
    });

    this.logger.log(`JWT strategy configured with issuer: ${issuerUrl}`);
    this.logger.log(`JWKS URI: ${jwksUri}`);
  }

  /**
   * Validates the decoded JWT payload and transforms it into an AuthenticatedUser.
   * Passport attaches the returned value to `request.user`.
   */
  validate(payload: KeycloakTokenPayload): AuthenticatedUser {
    if (!payload.sub) {
      throw new UnauthorizedException('Invalid token: missing subject claim');
    }

    const clientId = this.configService.get<string>(
      'KEYCLOAK_CLIENT_ID',
      'sentinel-api',
    );

    const realmRoles = payload.realm_access?.roles ?? [];
    const clientRoles = payload.resource_access?.[clientId]?.roles ?? [];
    const allRoles = [...new Set([...realmRoles, ...clientRoles])];

    const classificationLevel =
      payload.classification_level ?? 'UNCLASSIFIED';

    return {
      userId: payload.sub,
      username: payload.preferred_username,
      email: payload.email,
      name: payload.name,
      roles: allRoles,
      realmRoles,
      clientRoles,
      classificationLevel,
    };
  }
}
