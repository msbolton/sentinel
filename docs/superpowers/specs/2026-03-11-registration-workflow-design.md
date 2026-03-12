# Registration Workflow Design

## Overview

Self-service registration with admin approval for the Sentinel platform. Users submit a registration request with extended profile information; admins review and approve/reject via a custom panel. Keycloak remains the single source of truth for user management.

## Architecture: Keycloak Admin REST API Approach

All user data lives in Keycloak. The NestJS API gateway uses the `sentinel-service` client credentials to interact with Keycloak's Admin REST API for creating, querying, enabling, and deleting users. No additional database tables required.

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
7. Success: user sees confirmation screen — "Your request has been submitted. You'll receive an email when your account is approved."
8. Duplicate username/email: Keycloak returns 409 — surface friendly error

## Admin Approval Panel

1. New route `/admin/users` protected by `roleGuard('sentinel-admin')`
2. `PendingUsersComponent` — table listing disabled users that have a `registrationDate` custom attribute
3. Table columns: username, email, name, organization, justification, registration date
4. Each row has Approve and Reject buttons
5. **Approve**: NestJS endpoint calls Keycloak Admin API to:
   - Set `enabled: true`
   - Assign default roles: `sentinel-viewer`, `classification-u`
   - Trigger Keycloak's "execute actions" API with `VERIFY_EMAIL` action (sends built-in email)
6. **Reject**: NestJS endpoint deletes the Keycloak user and sends a rejection email via nodemailer
7. All endpoints require `sentinel-admin` role via `JwtAuthGuard`

### NestJS Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/auth/register` | Public | Create disabled user in Keycloak |
| `GET` | `/api/auth/pending-registrations` | `sentinel-admin` | List disabled users with `registrationDate` attribute |
| `POST` | `/api/auth/approve-registration/:userId` | `sentinel-admin` | Enable user, assign roles, send verification email |
| `POST` | `/api/auth/reject-registration/:userId` | `sentinel-admin` | Delete user, send rejection email |

## Email Notifications

- **On approval**: Keycloak's built-in `VERIFY_EMAIL` execute-actions email, sent via Keycloak SMTP
- **On rejection**: Custom email sent via nodemailer through the same SMTP server. Template: "Your Sentinel access request was not approved. Contact your administrator for details."
- **On registration**: No email — just in-app confirmation screen. Avoids emailing unverified addresses.

### SMTP Configuration

- **Dev**: MailHog at `mailhog:1025`, no auth. Web UI on port 8025.
- **Staging/Prod**: Configurable via environment variables

## Keycloak Realm Changes

1. `registrationAllowed` stays `false` — registration handled by our API, not Keycloak's built-in page
2. SMTP config added to `sentinel-realm.json` (dev: MailHog)
3. `sentinel-service` client's service account gets `manage-users` realm management role for Admin API access
4. MailHog service added to `docker-compose.infra.yml`

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
| `apps/api-gateway/src/modules/auth/keycloak-admin.service.ts` | Wraps Keycloak Admin REST API calls |
| `apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts` | Tests |
| `apps/api-gateway/src/modules/auth/mail.service.ts` | Rejection email via nodemailer |
| `apps/api-gateway/src/modules/auth/mail.service.spec.ts` | Tests |

### Modified Files

| File | Change |
|------|--------|
| `apps/web/src/app/app.routes.ts` | Add `/register` and `/admin/users` routes |
| `apps/web/src/app/features/login/login.component.ts` | Add "Request Access" link |
| `config/keycloak/sentinel-realm.json` | Add SMTP config, `manage-users` role for service account |
| `docker-compose.infra.yml` | Add MailHog service |
| `apps/api-gateway/src/modules/auth/auth.module.ts` | Register new controller and services |

### New Dependencies

- `nodemailer` — SMTP email sending for rejection notifications
- `@types/nodemailer` — TypeScript types

## Roles & Defaults

- New users receive: `sentinel-viewer` + `classification-u` on approval
- Admins can change roles later through Keycloak admin console or a future role management panel
- Registration form does not expose role selection

## Security Considerations

- Registration endpoint is public but rate-limited (standard NestJS throttling)
- Passwords handled by Keycloak (hashing, policy enforcement)
- Custom attributes (organization, justification) are not exposed in tokens — admin-only via Admin API
- Brute force protection on login already enabled in realm config
