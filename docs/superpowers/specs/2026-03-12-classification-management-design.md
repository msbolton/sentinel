# Classification Level Management — Design Spec

## Goal

Allow admins to assign classification levels during user approval and change them at any time after. Introduce a Settings page accessible via the sidebar gear icon, with a Profile tab (all users) and a User Management tab (admins only).

## Classification Levels

| Display Name | Role Name |
|---|---|
| UNCLASSIFIED | `classification-u` |
| SECRET | `classification-s` |
| TOP SECRET | `classification-ts` |

These roles already exist in the Keycloak realm. No new roles needed.

## Settings Page

New route: `/settings` — replaces the current no-op gear icon behavior. Renders as a full page (using the `isPageRoute` pattern, same as `/admin/*`). Two tabs:

### Profile Tab (all users, default)

- Read-only display of current user info from `AuthService.userProfile$`
- Shows: username, email, roles (as badges), classification level
- No edit functionality — just informational

### User Management Tab (admins only)

- Tab only visible/accessible when user has `sentinel-admin` role
- Contains two sections:

#### 1. Pending Registrations (moved from `/admin/users`)

- Same table as today with username, email, name, organization, justification, date
- **New:** Classification dropdown per row, defaults to UNCLASSIFIED
- Approve sends the selected classification level to the backend
- Reject unchanged

#### 2. Active Users

- Table showing all enabled, non-service-account users
- Columns: username, email, name, classification level (dropdown)
- Classification dropdown shows the user's current level and allows changing it
- On change, immediately calls the backend to update — no separate save button
- Loading/error states per row during update

## Route Changes

| Route | Change |
|---|---|
| `/settings` | New — lazy-loaded `SettingsComponent`, guarded by `authGuard` |
| `/admin/users` | Remove — functionality moves into settings |

The gear icon in the sidebar changes from `(click)="toggleSettings()"` to `routerLink="/settings"`.

The `isPageRoute` check in `AppComponent` expands to match `/settings` in addition to `/admin`.

## Backend Changes

### Modified: `POST /api/v1/auth/approve-registration/:userId`

Request body gains an optional `classificationLevel` field:

```json
{ "classificationLevel": "classification-s" }
```

- If omitted, defaults to `classification-u`
- `approveUser()` in `KeycloakAdminService` uses this value instead of hardcoded `classification-u`
- Validation: must be one of `classification-u`, `classification-s`, `classification-ts`

### New: `GET /api/v1/auth/users`

- Guarded: `JwtAuthGuard` + `@Roles('sentinel-admin')`
- Returns all enabled users (excluding service accounts) with their current roles
- Response shape:
  ```json
  [
    {
      "id": "uuid",
      "username": "jdoe",
      "email": "jdoe@example.com",
      "firstName": "John",
      "lastName": "Doe",
      "classificationLevel": "classification-u",
      "roles": ["sentinel-viewer", "classification-u"]
    }
  ]
  ```
- `classificationLevel` is derived by finding the first `classification-*` role in the user's realm roles

### New: `PUT /api/v1/auth/users/:userId/classification`

- Guarded: `JwtAuthGuard` + `@Roles('sentinel-admin')`
- Request body:
  ```json
  { "classificationLevel": "classification-ts" }
  ```
- Validation: must be one of `classification-u`, `classification-s`, `classification-ts`
- Implementation:
  1. Fetch user's current realm role mappings
  2. Find and remove any existing `classification-*` role
  3. Assign the new classification role
  4. Return `{ message: "Classification updated" }`

### Modified: `KeycloakAdminService`

- `approveUser(userId, classificationLevel)` — second parameter replaces hardcoded `classification-u`
- New method `getActiveUsers()` — fetches enabled users, filters out service accounts, maps roles
- New method `updateClassification(userId, classificationLevel)` — swaps classification role

## File Changes

| File | Change |
|---|---|
| `keycloak-admin.service.ts` | Modify `approveUser`, add `getActiveUsers`, add `updateClassification` |
| `keycloak-admin.service.spec.ts` | Tests for modified/new methods |
| `registration.controller.ts` | Modify approve endpoint body, add `GET /users`, add `PUT /users/:userId/classification` |
| `registration.controller.spec.ts` | Tests for modified/new endpoints |
| `settings.component.ts` | New — settings page with Profile + User Management tabs |
| `settings.component.spec.ts` | New — tests for tab rendering, profile display, classification management |
| `app.routes.ts` | Add `/settings` route, remove `/admin/users` route |
| `app.component.ts` | Change gear icon to routerLink, update `isPageRoute` to include `/settings`, remove admin sidebar button |

The existing `pending-users.component.ts` is deleted — its functionality is absorbed into `settings.component.ts`.

## Validation

The set of valid classification levels is defined once as a constant:

```typescript
const VALID_CLASSIFICATIONS = ['classification-u', 'classification-s', 'classification-ts'] as const;
```

Both the approve and update-classification endpoints validate against this set. Invalid values return 400.

## Edge Cases

- **User has no classification role:** `getActiveUsers` returns `classificationLevel: null`, UI shows "None" and allows assigning one
- **User has multiple classification roles:** Use the highest one (ts > s > u), remove the others on next update
- **Admin changes own classification:** Allowed — no self-edit restriction needed since admin role is separate from classification
- **Concurrent updates:** Last write wins — acceptable for low-frequency admin operations
- **Non-admin navigates to `/settings`:** Sees only Profile tab; User Management tab is hidden
