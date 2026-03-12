# Registration Workflow Design

## Overview

Self-service registration with admin approval for the Sentinel platform. Users submit a registration request with extended profile information; admins review and approve/reject via a custom panel. Keycloak remains the single source of truth for user management.

## Architecture: Keycloak Admin REST API Approach

All user data lives in Keycloak. The NestJS API gateway uses the `sentinel-service` client credentials to interact with Keycloak's Admin REST API for creating, querying, enabling, and deleting users. No additional database tables required.

The `KeycloakAdminService` obtains a client credentials token on startup and caches it, refreshing 30 seconds before expiry using the `expires_in` value from the token response. All Admin API calls go through this service.

## Registration Flow

1. User clicks "Request Access" link on the login page (`/login`)
2. Navigates to `/register` — a new `RegisterComponent` with the same branded styling as login
3. Form fields:
   - Username (required)
   - Email (required, validated format)
   - Password (required, min 8 chars)
   - Confirm password (required, must match)
   - First name (required)
   - Last name (required)
   - Organization/unit (required)
   - Justification for access (required, free-text)
4. Client-side validation: required fields, email format, password match, minimum password length (8 chars)
5. On submit: `POST /api/auth/register` to NestJS API gateway
6. API gateway uses `sentinel-service` client credentials to call Keycloak Admin REST API:
   - Create user with `enabled: false`
   - Set custom attributes: `organization`, `justification`, `registrationDate`
   - Set credentials (password)
   - If Keycloak rejects the password (policy violation), the error is parsed and surfaced to the user as a 400 with a descriptive message
7. Success: user sees confirmation screen — "Your request has been submitted. You'll receive an email when your account is approved."
8. Duplicate username/email: Keycloak returns 409 — surface friendly error

### Rate Limiting

The `POST /api/auth/register` endpoint is the only public POST that creates a persistent resource. It must be rate-limited more aggressively than other endpoints:
- **5 requests per IP per 15-minute window** using `@nestjs/throttler` with IP-based keying
- Returns 429 with a "Too many registration attempts. Please try again later." message

## Admin Approval Panel

1. New route `/admin/users` protected by `roleGuard('sentinel-admin')`
2. `PendingUsersComponent` — table listing disabled users that have a `registrationDate` custom attribute (this distinguishes pending registrations from users disabled for other reasons)
3. Table columns: username, email, name, organization, justification, registration date
4. Each row has Approve and Reject buttons with loading states (spinner on the active button, other buttons disabled)
5. **Approve**: NestJS endpoint calls Keycloak Admin API to:
   - Set `enabled: true`
   - Assign default roles: `sentinel-viewer`, `classification-u`
   - Remove the `registrationDate` attribute (so the user no longer appears as "pending")
   - Trigger Keycloak's "execute actions" API with `VERIFY_EMAIL` action (sends built-in email)
   - On success: row is removed from the table with a success toast
   - On failure: error toast with message, button returns to normal state
6. **Reject**: NestJS endpoint deletes the Keycloak user and sends a rejection email via `@nestjs/mailer`
   - On success: row is removed from the table with a confirmation toast
   - On failure: error toast with message
7. All endpoints require `sentinel-admin` role via `JwtAuthGuard`
8. The `PendingUsersComponent` calls `GET /api/auth/pending-registrations` to load data

**Note on dev mode**: In dev mode, the NestJS `JwtAuthGuard` bypasses auth and uses `DEV_USER` which includes `sentinel-admin`, so the backend endpoints are accessible. However, the Angular `AuthService.setDevelopmentProfile()` currently sets roles to `['sentinel-analyst', 'sentinel-operator']` which does not include `sentinel-admin`. The dev profile must be updated to include `sentinel-admin` so the frontend `roleGuard` allows access to `/admin/users` in dev mode.

### Pagination

The `GET /api/auth/pending-registrations` endpoint passes Keycloak's `max` query param capped at 100 results. For v1, this is a simple unpaginated list with a hard cap. If the list grows beyond 100, the admin can manage users directly in Keycloak's admin console as a fallback. Pagination can be added in a future iteration.

### NestJS Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/auth/register` | Public (rate-limited) | Create disabled user in Keycloak |
| `GET` | `/api/auth/pending-registrations` | `sentinel-admin` | List disabled users with `registrationDate` attribute (max 100) |
| `POST` | `/api/auth/approve-registration/:userId` | `sentinel-admin` | Enable user, assign roles, send verification email |
| `POST` | `/api/auth/reject-registration/:userId` | `sentinel-admin` | Delete user, send rejection email |

## Email Notifications

- **On approval**: Keycloak's built-in `VERIFY_EMAIL` execute-actions email, sent via Keycloak SMTP
- **On rejection**: Custom email sent via `@nestjs/mailer` through the same SMTP server. Template: "Your Sentinel access request was not approved. Contact your administrator for details." Note: this email goes to an unverified address — this is an accepted trade-off since the rejection is a courtesy notification with no security-sensitive content.
- **On registration**: No email — just in-app confirmation screen. Avoids emailing unverified addresses for security-relevant flows.

### SMTP Configuration

- **Dev**: MailHog at `mailhog:1025`, no auth. Web UI on port 8025.
- **Staging/Prod**: Configurable via environment variables

## Keycloak Realm Changes

1. `registrationAllowed` stays `false` — registration handled by our API, not Keycloak's built-in page
2. SMTP config added to `sentinel-realm.json` (dev: MailHog)
3. `sentinel-service` client's service account gets `manage-users` realm management role for Admin API access
4. MailHog service added to `docker-compose.infra.yml`
5. Confirm `classification-u` role exists in `sentinel-realm.json` — it is already defined in the realm roles list

## File Changes

### New Files

| File | Purpose |
|------|---------|
| `apps/web/src/app/features/register/register.component.ts` | Registration form (standalone, branded) |
| `apps/web/src/app/features/register/register.component.spec.ts` | Tests |
| `apps/web/src/app/features/admin/pending-users/pending-users.component.ts` | Admin approval table |
| `apps/web/src/app/features/admin/pending-users/pending-users.component.spec.ts` | Tests |
| `apps/api-gateway/src/modules/auth/registration.controller.ts` | Register, approve, reject endpoints |
| `apps/api-gateway/src/modules/auth/registration.controller.spec.ts` | Tests |
| `apps/api-gateway/src/modules/auth/keycloak-admin.service.ts` | Wraps Keycloak Admin REST API calls (token caching + refresh) |
| `apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts` | Tests |

### Modified Files

| File | Change |
|------|--------|
| `apps/web/src/app/app.routes.ts` | Add `/register` and `/admin/users` routes |
| `apps/web/src/app/app.component.ts` | Rename `isLoginRoute` signal to `isFullScreenRoute` and update the check to `['/login', '/register'].includes(url)` so both pages render without the app shell |
| `apps/web/src/app/core/services/auth.service.ts` | Add `sentinel-admin` to `setDevelopmentProfile()` roles array |
| `apps/web/src/app/features/login/login.component.ts` | Add "Request Access" link |
| `config/keycloak/sentinel-realm.json` | Add SMTP config, `manage-users` role for service account |
| `docker-compose.infra.yml` | Add MailHog service |
| `apps/api-gateway/src/modules/auth/auth.module.ts` | Register new controller, services, and `@nestjs/mailer` |

### New Dependencies

- `@nestjs/throttler` — rate limiting for the public registration endpoint; `ThrottlerModule` registered in `auth.module.ts`
- `@nestjs/mailer` — NestJS-native mail module (wraps nodemailer with Handlebars templates)
- `nodemailer` — transitive dependency of `@nestjs/mailer`
- `@types/nodemailer` — TypeScript types

## Roles & Defaults

- New users receive: `sentinel-viewer` + `classification-u` on approval
- Admins can change roles later through Keycloak admin console or a future role management panel
- Registration form does not expose role selection

## Security Considerations

- Registration endpoint is public with aggressive rate limiting (5 requests per IP per 15 minutes)
- Password policy: Keycloak enforces its password policy when credentials are set via Admin API (verified in Keycloak 24). The server-side endpoint also validates minimum length as a fast-fail before calling Keycloak, and surfaces any Keycloak password policy errors as user-friendly 400 responses.
- Custom attributes (organization, justification) are not exposed in tokens — admin-only via Admin API
- Brute force protection on login already enabled in realm config
- Disabled users who attempt login see Keycloak's standard "Account is disabled" error — no additional messaging needed since pendingusers have not been told their credentials work

## Edge Cases

- **Admin disables an approved user in Keycloak console**: The user will not appear in the pending registrations list (no `registrationDate` attribute). They get Keycloak's standard "Account is disabled" error on login.
- **Duplicate registration attempts**: If a user registers, gets rejected (deleted), and re-registers, this is treated as a fresh registration.
- **Keycloak Admin API unavailable**: Registration endpoint returns 503 "Service temporarily unavailable." Admin panel shows an error state.
