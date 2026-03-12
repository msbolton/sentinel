# Classification Level Management Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow admins to assign classification levels during user approval and change them anytime after. Introduce a Settings page with Profile and User Management tabs.

**Architecture:** Extend `KeycloakAdminService` with classification-aware approval, active user listing, and classification update methods. Extend `RegistrationController` with two new endpoints. Create a new `SettingsComponent` at `/settings` with a Profile tab (all users) and a User Management tab (admins only) that absorbs the existing pending-users functionality. Remove the old `/admin/users` route and `PendingUsersComponent`.

**Tech Stack:** NestJS, Angular 19 (standalone, OnPush, signals), Keycloak Admin REST API

---

## Chunk 1: Backend

### Task 1: Add `VALID_CLASSIFICATIONS` constant and modify `approveUser`

**Files:**
- Modify: `apps/api-gateway/src/modules/auth/keycloak-admin.service.ts`
- Modify: `apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts`

**Context:** `approveUser` currently hardcodes `classification-u`. It needs a `classificationLevel` parameter. The constant is used for validation in both the service and controller.

- [ ] **Step 1: Add the constant at the top of the service file**

```typescript
export const VALID_CLASSIFICATIONS = ['classification-u', 'classification-s', 'classification-ts'] as const;
export type ClassificationLevel = typeof VALID_CLASSIFICATIONS[number];
```

- [ ] **Step 2: Write failing test for `approveUser` with classification parameter**

Add to the existing `approveUser` describe block:

```typescript
it('should assign the specified classification role instead of classification-u', async () => {
  // ... setup mocks same as existing approve test ...
  await service.approveUser('user-1', 'classification-ts');

  // Verify the role assignment call includes classification-ts, not classification-u
  const assignCall = (global.fetch as jest.Mock).mock.calls.find(
    (c: any[]) => c[0].includes('role-mappings/realm') && c[1]?.method === 'POST',
  );
  const assignedRoles = JSON.parse(assignCall[1].body);
  expect(assignedRoles.some((r: any) => r.name === 'classification-ts')).toBe(true);
  expect(assignedRoles.some((r: any) => r.name === 'classification-u')).toBe(false);
});

it('should default to classification-u when classificationLevel is omitted', async () => {
  // ... setup mocks ...
  await service.approveUser('user-1');

  const assignCall = (global.fetch as jest.Mock).mock.calls.find(
    (c: any[]) => c[0].includes('role-mappings/realm') && c[1]?.method === 'POST',
  );
  const assignedRoles = JSON.parse(assignCall[1].body);
  expect(assignedRoles.some((r: any) => r.name === 'classification-u')).toBe(true);
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `npx nx test api-gateway --testFile=apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts`

- [ ] **Step 4: Update `approveUser` signature and role filter**

Change signature from:
```typescript
async approveUser(userId: string): Promise<void> {
```
To:
```typescript
async approveUser(userId: string, classificationLevel: ClassificationLevel = 'classification-u'): Promise<void> {
```

Change the role filter from:
```typescript
const rolesToAssign = allRoles.filter(
  (r) => r.name === 'sentinel-viewer' || r.name === 'classification-u',
);
```
To:
```typescript
const rolesToAssign = allRoles.filter(
  (r) => r.name === 'sentinel-viewer' || r.name === classificationLevel,
);
```

Update the error message similarly:
```typescript
throw new Error(`Required roles (sentinel-viewer, ${classificationLevel}) not found`);
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `npx nx test api-gateway --testFile=apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts`

- [ ] **Step 6: Commit**

```bash
git add apps/api-gateway/src/modules/auth/keycloak-admin.service.ts apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts
git commit -m "feat(auth): add classification level parameter to approveUser"
```

---

### Task 2: Add `getActiveUsers` method

**Files:**
- Modify: `apps/api-gateway/src/modules/auth/keycloak-admin.service.ts`
- Modify: `apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts`

**Context:** New method to list all enabled users with their classification level. Filters out service accounts (usernames starting with `service-account-`). Derives classification from realm role mappings.

- [ ] **Step 1: Write failing tests**

```typescript
describe('getActiveUsers', () => {
  it('should return enabled users with their classification level', async () => {
    // Mock GET /users?enabled=true&max=100 → list of users
    // Mock GET /users/{id}/role-mappings/realm → roles including classification-s
    // Assert result includes classificationLevel: 'classification-s'
  });

  it('should exclude service account users', async () => {
    // Include a user with username 'service-account-sentinel-service' in the mock
    // Assert it is not in the result
  });

  it('should return classificationLevel as null when user has no classification role', async () => {
    // Mock role mappings without any classification-* role
    // Assert classificationLevel is null
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

- [ ] **Step 3: Implement `getActiveUsers`**

```typescript
export interface ActiveUser {
  id: string;
  username: string;
  email: string;
  firstName: string;
  lastName: string;
  classificationLevel: ClassificationLevel | null;
  roles: string[];
}

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
```

- [ ] **Step 4: Run tests to verify they pass**

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/auth/keycloak-admin.service.ts apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts
git commit -m "feat(auth): add getActiveUsers method to KeycloakAdminService"
```

---

### Task 3: Add `updateClassification` method

**Files:**
- Modify: `apps/api-gateway/src/modules/auth/keycloak-admin.service.ts`
- Modify: `apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts`

**Context:** Swaps a user's classification role: removes any existing `classification-*` roles, then assigns the new one.

- [ ] **Step 1: Write failing tests**

```typescript
describe('updateClassification', () => {
  it('should remove old classification role and assign new one', async () => {
    // Mock GET /users/{id}/role-mappings/realm → [{ id: 'r1', name: 'classification-u' }, { id: 'r2', name: 'sentinel-viewer' }]
    // Mock DELETE /users/{id}/role-mappings/realm → 204
    // Mock POST /users/{id}/role-mappings/realm → 204
    // Mock GET /roles → all roles
    await service.updateClassification('user-1', 'classification-ts');
    // Assert DELETE was called with classification-u role
    // Assert POST was called with classification-ts role
  });

  it('should work when user has no existing classification role', async () => {
    // Mock role mappings without classification-* roles
    // Should skip the DELETE, only POST the new role
  });

  it('should throw on invalid classification level', async () => {
    await expect(service.updateClassification('user-1', 'classification-x' as any))
      .rejects.toThrow();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

- [ ] **Step 3: Implement `updateClassification`**

```typescript
async updateClassification(userId: string, classificationLevel: ClassificationLevel): Promise<void> {
  if (!VALID_CLASSIFICATIONS.includes(classificationLevel)) {
    throw new HttpException('Invalid classification level', HttpStatus.BAD_REQUEST);
  }

  // Get current role mappings
  const rolesResponse = await this.adminRequest(`/users/${userId}/role-mappings/realm`);
  if (!rolesResponse.ok) {
    throw new HttpException('Failed to fetch user roles', HttpStatus.INTERNAL_SERVER_ERROR);
  }

  const currentRoles = (await rolesResponse.json()) as Array<{ id: string; name: string }>;
  const existingClassificationRoles = currentRoles.filter(r =>
    VALID_CLASSIFICATIONS.includes(r.name as ClassificationLevel),
  );

  // Remove existing classification roles
  if (existingClassificationRoles.length > 0) {
    const removeResponse = await this.adminRequest(`/users/${userId}/role-mappings/realm`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(existingClassificationRoles),
    });
    if (!removeResponse.ok) {
      throw new HttpException('Failed to remove existing classification', HttpStatus.INTERNAL_SERVER_ERROR);
    }
  }

  // Fetch all roles to get the new role's ID
  const allRolesResponse = await this.adminRequest('/roles');
  if (!allRolesResponse.ok) {
    throw new HttpException('Failed to fetch roles', HttpStatus.INTERNAL_SERVER_ERROR);
  }

  const allRoles = (await allRolesResponse.json()) as Array<{ id: string; name: string }>;
  const newRole = allRoles.find(r => r.name === classificationLevel);
  if (!newRole) {
    throw new HttpException(`Role ${classificationLevel} not found`, HttpStatus.INTERNAL_SERVER_ERROR);
  }

  const assignResponse = await this.adminRequest(`/users/${userId}/role-mappings/realm`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify([newRole]),
  });

  if (!assignResponse.ok) {
    throw new HttpException('Failed to assign classification', HttpStatus.INTERNAL_SERVER_ERROR);
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/auth/keycloak-admin.service.ts apps/api-gateway/src/modules/auth/keycloak-admin.service.spec.ts
git commit -m "feat(auth): add updateClassification method to KeycloakAdminService"
```

---

### Task 4: Add controller endpoints

**Files:**
- Modify: `apps/api-gateway/src/modules/auth/registration.controller.ts`
- Modify: `apps/api-gateway/src/modules/auth/registration.controller.spec.ts`

**Context:** Three changes: (1) approve endpoint accepts `classificationLevel` in body, (2) new `GET /auth/users` endpoint, (3) new `PUT /auth/users/:userId/classification` endpoint.

- [ ] **Step 1: Write failing tests**

Add to controller spec:

```typescript
describe('POST /approve-registration/:userId with classification', () => {
  it('should pass classificationLevel to approveUser', async () => {
    await controller.approveRegistration('user-1', { classificationLevel: 'classification-ts' });
    expect(keycloakAdmin.approveUser).toHaveBeenCalledWith('user-1', 'classification-ts');
  });

  it('should default to classification-u when classificationLevel not provided', async () => {
    await controller.approveRegistration('user-1', {});
    expect(keycloakAdmin.approveUser).toHaveBeenCalledWith('user-1', 'classification-u');
  });

  it('should reject invalid classification level', async () => {
    await expect(controller.approveRegistration('user-1', { classificationLevel: 'classification-x' }))
      .rejects.toThrow();
  });
});

describe('GET /users', () => {
  it('should return active users from keycloakAdmin', async () => {
    const mockUsers = [{ id: 'u1', username: 'jdoe', classificationLevel: 'classification-u' }];
    keycloakAdmin.getActiveUsers.mockResolvedValue(mockUsers);
    const result = await controller.getUsers();
    expect(result).toEqual(mockUsers);
  });
});

describe('PUT /users/:userId/classification', () => {
  it('should call updateClassification and return success message', async () => {
    keycloakAdmin.updateClassification.mockResolvedValue(undefined);
    const result = await controller.updateClassification('user-1', { classificationLevel: 'classification-ts' });
    expect(keycloakAdmin.updateClassification).toHaveBeenCalledWith('user-1', 'classification-ts');
    expect(result.message).toContain('updated');
  });

  it('should reject invalid classification level', async () => {
    await expect(controller.updateClassification('user-1', { classificationLevel: 'bad' as any }))
      .rejects.toThrow();
  });
});
```

Add `getActiveUsers` and `updateClassification` to the mock `keycloakAdmin` object:
```typescript
keycloakAdmin = {
  createUser: jest.fn().mockResolvedValue(undefined),
  getPendingRegistrations: jest.fn().mockResolvedValue([]),
  approveUser: jest.fn().mockResolvedValue(undefined),
  rejectUser: jest.fn().mockResolvedValue({ email: 'user@test.com', firstName: 'Test' }),
  getActiveUsers: jest.fn().mockResolvedValue([]),
  updateClassification: jest.fn().mockResolvedValue(undefined),
};
```

- [ ] **Step 2: Run tests to verify they fail**

- [ ] **Step 3: Implement the changes**

Import `VALID_CLASSIFICATIONS` and add `Put` to the NestJS imports:

```typescript
import { Controller, Post, Get, Put, Body, Param, UseGuards, HttpException, HttpStatus, Logger } from '@nestjs/common';
import { VALID_CLASSIFICATIONS, ClassificationLevel } from './keycloak-admin.service';
```

Add a helper interface:
```typescript
interface ClassificationDto {
  classificationLevel?: string;
}
```

Modify `approveRegistration`:
```typescript
@Post('approve-registration/:userId')
@UseGuards(JwtAuthGuard)
@Roles('sentinel-admin')
async approveRegistration(
  @Param('userId') userId: string,
  @Body() body: ClassificationDto,
): Promise<{ message: string }> {
  const level = body.classificationLevel ?? 'classification-u';
  if (!VALID_CLASSIFICATIONS.includes(level as ClassificationLevel)) {
    throw new HttpException('Invalid classification level', HttpStatus.BAD_REQUEST);
  }
  await this.keycloakAdmin.approveUser(userId, level as ClassificationLevel);
  this.logger.log(`User ${userId} approved with ${level}`);
  return { message: `User ${userId} has been approved.` };
}
```

Add new endpoints:
```typescript
@Get('users')
@UseGuards(JwtAuthGuard)
@Roles('sentinel-admin')
async getUsers() {
  return this.keycloakAdmin.getActiveUsers();
}

@Put('users/:userId/classification')
@UseGuards(JwtAuthGuard)
@Roles('sentinel-admin')
async updateClassification(
  @Param('userId') userId: string,
  @Body() body: ClassificationDto,
): Promise<{ message: string }> {
  if (!body.classificationLevel || !VALID_CLASSIFICATIONS.includes(body.classificationLevel as ClassificationLevel)) {
    throw new HttpException('Invalid classification level', HttpStatus.BAD_REQUEST);
  }
  await this.keycloakAdmin.updateClassification(userId, body.classificationLevel as ClassificationLevel);
  this.logger.log(`User ${userId} classification updated to ${body.classificationLevel}`);
  return { message: `Classification updated for user ${userId}.` };
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test api-gateway`

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/auth/registration.controller.ts apps/api-gateway/src/modules/auth/registration.controller.spec.ts
git commit -m "feat(auth): add classification endpoints and update approve to accept level"
```

---

## Chunk 2: Frontend

### Task 5: Create SettingsComponent with Profile and User Management tabs

**Files:**
- Create: `apps/web/src/app/features/settings/settings.component.ts`
- Create: `apps/web/src/app/features/settings/settings.component.spec.ts`

**Context:** New `/settings` page replaces the old `/admin/users` page. Two tabs:
- **Profile** (default, all users): read-only display of current user info from `AuthService.userProfile$` — username, email, roles (as badges), classification level.
- **User Management** (admin only, visible when user has `sentinel-admin` role): contains two sections — Pending Registrations (moved from old `PendingUsersComponent` with new classification dropdown per row) and Active Users (table with inline classification dropdown).

Classification options display as human-readable labels: UNCLASSIFIED, SECRET, TOP SECRET — mapping to role values `classification-u`, `classification-s`, `classification-ts`.

API endpoints:
- `GET /api/v1/auth/pending-registrations` — pending users (existing)
- `POST /api/v1/auth/approve-registration/:userId` — body now accepts `{ classificationLevel: string }`
- `POST /api/v1/auth/reject-registration/:userId` — reject (existing, unchanged)
- `GET /api/v1/auth/users` — active users with `classificationLevel` field (new, Task 4)
- `PUT /api/v1/auth/users/:userId/classification` — update classification (new, Task 4)

The component reads `AuthService.userProfile$` via `toSignal` to get the current user profile for the Profile tab and to determine whether the User Management tab should be visible (check for `sentinel-admin` in roles).

- [ ] **Step 1: Write the test file**

Create `apps/web/src/app/features/settings/settings.component.spec.ts`:

```typescript
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { SettingsComponent } from './settings.component';
import { AuthService } from '../../core/services/auth.service';
import { BehaviorSubject } from 'rxjs';

const mockProfile = {
  username: 'admin',
  email: 'admin@sentinel.local',
  roles: ['sentinel-admin', 'sentinel-analyst', 'classification-ts'],
  classificationLevel: 'TOP SECRET',
};

const mockNonAdminProfile = {
  username: 'viewer',
  email: 'viewer@sentinel.local',
  roles: ['sentinel-viewer', 'classification-u'],
  classificationLevel: 'UNCLASSIFIED',
};

describe('SettingsComponent', () => {
  let component: SettingsComponent;
  let fixture: ComponentFixture<SettingsComponent>;
  let httpMock: HttpTestingController;
  let profileSubject: BehaviorSubject<any>;

  function setup(profile = mockProfile) {
    profileSubject = new BehaviorSubject(profile);

    TestBed.configureTestingModule({
      imports: [SettingsComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: AuthService,
          useValue: {
            userProfile$: profileSubject.asObservable(),
          },
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(SettingsComponent);
    component = fixture.componentInstance;
    httpMock = TestBed.inject(HttpTestingController);
  }

  afterEach(() => {
    httpMock.verify();
  });

  describe('Profile tab', () => {
    beforeEach(() => setup());

    it('should default to profile tab', () => {
      fixture.detectChanges();
      expect(component.activeTab()).toBe('profile');
    });

    it('should display user profile info', () => {
      fixture.detectChanges();
      const el = fixture.nativeElement;
      expect(el.textContent).toContain('admin');
      expect(el.textContent).toContain('admin@sentinel.local');
    });
  });

  describe('User Management tab (admin)', () => {
    beforeEach(() => setup());

    it('should show User Management tab for admin users', () => {
      fixture.detectChanges();
      const tabs = fixture.nativeElement.querySelectorAll('.tab-btn');
      expect(tabs.length).toBe(2);
      expect(tabs[1].textContent).toContain('User Management');
    });

    it('should load pending and active users when switching to management tab', () => {
      fixture.detectChanges();
      component.activeTab.set('management');
      fixture.detectChanges();

      const pendingReq = httpMock.expectOne('/api/v1/auth/pending-registrations');
      expect(pendingReq.request.method).toBe('GET');
      pendingReq.flush([]);

      const usersReq = httpMock.expectOne('/api/v1/auth/users');
      expect(usersReq.request.method).toBe('GET');
      usersReq.flush([]);
    });

    it('should send classification level when approving', () => {
      component.activeTab.set('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([
        { id: 'u1', username: 'p1', email: 'p@t.com', firstName: 'P', lastName: '1', organization: 'O', justification: 'J', registrationDate: '2026-03-11' },
      ]);
      httpMock.expectOne('/api/v1/auth/users').flush([]);
      fixture.detectChanges();

      component.setPendingClassification('u1', 'classification-ts');
      component.approve('u1');

      const approveReq = httpMock.expectOne('/api/v1/auth/approve-registration/u1');
      expect(approveReq.request.body).toEqual({ classificationLevel: 'classification-ts' });
      approveReq.flush({});
    });

    it('should load active users and allow classification change', () => {
      component.activeTab.set('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([]);
      httpMock.expectOne('/api/v1/auth/users').flush([
        { id: 'a1', username: 'jdoe', email: 'j@e.com', firstName: 'J', lastName: 'D', classificationLevel: 'classification-u', roles: [] },
      ]);
      fixture.detectChanges();

      component.updateClassification('a1', 'classification-ts');

      const updateReq = httpMock.expectOne('/api/v1/auth/users/a1/classification');
      expect(updateReq.request.method).toBe('PUT');
      expect(updateReq.request.body).toEqual({ classificationLevel: 'classification-ts' });
      updateReq.flush({});

      expect(component.activeUsers()[0].classificationLevel).toBe('classification-ts');
    });

    it('should reject a user and remove from list', () => {
      component.activeTab.set('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([
        { id: 'u1', username: 'p1', email: 'p@t.com', firstName: 'P', lastName: '1', organization: 'O', justification: 'J', registrationDate: '2026-03-11' },
      ]);
      httpMock.expectOne('/api/v1/auth/users').flush([]);
      fixture.detectChanges();

      component.reject('u1');

      const rejectReq = httpMock.expectOne('/api/v1/auth/reject-registration/u1');
      expect(rejectReq.request.method).toBe('POST');
      rejectReq.flush({});

      expect(component.pendingUsers().length).toBe(0);
    });

    it('should show error when loading pending users fails', () => {
      component.activeTab.set('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations')
        .flush({ message: 'Server Error' }, { status: 500, statusText: 'Error' });
      httpMock.expectOne('/api/v1/auth/users').flush([]);
      fixture.detectChanges();

      expect(component.errorMessage()).toBeTruthy();
    });

    it('should handle active user with null classificationLevel', () => {
      component.activeTab.set('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([]);
      httpMock.expectOne('/api/v1/auth/users').flush([
        { id: 'a1', username: 'newuser', email: 'n@e.com', firstName: 'N', lastName: 'U', classificationLevel: null, roles: [] },
      ]);
      fixture.detectChanges();

      expect(component.activeUsers()[0].classificationLevel).toBeNull();
    });

    it('should not reload data on repeated tab switches', () => {
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([]);
      httpMock.expectOne('/api/v1/auth/users').flush([]);

      component.switchTab('profile');
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectNone('/api/v1/auth/pending-registrations');
      httpMock.expectNone('/api/v1/auth/users');
    });
  });

  describe('Non-admin user', () => {
    beforeEach(() => setup(mockNonAdminProfile));

    it('should NOT show User Management tab for non-admin', () => {
      fixture.detectChanges();
      const tabs = fixture.nativeElement.querySelectorAll('.tab-btn');
      expect(tabs.length).toBe(1);
      expect(tabs[0].textContent).toContain('Profile');
    });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test web --testFile=apps/web/src/app/features/settings/settings.component.spec.ts`
Expected: FAIL — `SettingsComponent` does not exist.

- [ ] **Step 3: Create the SettingsComponent**

Create `apps/web/src/app/features/settings/settings.component.ts`:

```typescript
import { Component, inject, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { DatePipe, UpperCasePipe } from '@angular/common';
import { HttpClient } from '@angular/common/http';
import { toSignal } from '@angular/core/rxjs-interop';
import { AuthService } from '../../core/services/auth.service';

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

interface ActiveUser {
  id: string;
  username: string;
  email: string;
  firstName: string;
  lastName: string;
  classificationLevel: string | null;
  roles: string[];
}

@Component({
  selector: 'app-settings',
  standalone: true,
  imports: [DatePipe, UpperCasePipe],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="settings-page">
      <div class="settings-header">
        <h1 class="settings-title">Settings</h1>
      </div>

      <div class="tabs">
        <button
          class="tab-btn"
          [class.active]="activeTab() === 'profile'"
          (click)="switchTab('profile')">
          Profile
        </button>
        @if (isAdmin()) {
          <button
            class="tab-btn"
            [class.active]="activeTab() === 'management'"
            (click)="switchTab('management')">
            User Management
          </button>
        }
      </div>

      @if (activeTab() === 'profile') {
        <!-- Profile Tab -->
        <div class="panel">
          @if (profile(); as p) {
            <div class="profile-section">
              <div class="profile-avatar">
                {{ (p.username ?? '?')[0] | uppercase }}
              </div>
              <div class="profile-info">
                <div class="info-row">
                  <span class="info-label">USERNAME</span>
                  <span class="info-value monospace">{{ p.username }}</span>
                </div>
                <div class="info-row">
                  <span class="info-label">EMAIL</span>
                  <span class="info-value">{{ p.email ?? '—' }}</span>
                </div>
                <div class="info-row">
                  <span class="info-label">CLASSIFICATION</span>
                  <span class="info-value classification">{{ p.classificationLevel }}</span>
                </div>
                <div class="info-row">
                  <span class="info-label">ROLES</span>
                  <div class="role-badges">
                    @for (role of sentinelRoles(); track role) {
                      <span class="role-badge">{{ role }}</span>
                    }
                  </div>
                </div>
              </div>
            </div>
          } @else {
            <div class="empty-state">No profile information available.</div>
          }
        </div>
      }

      @if (activeTab() === 'management' && isAdmin()) {
        <!-- User Management Tab -->

        @if (errorMessage()) {
          <div class="toast toast-error">{{ errorMessage() }}</div>
        }
        @if (successMessage()) {
          <div class="toast toast-success">{{ successMessage() }}</div>
        }

        <!-- Pending Registrations -->
        <div class="panel">
          <div class="panel-header">
            <h2 class="panel-title">Pending Registrations</h2>
            <button class="refresh-btn" (click)="loadPendingUsers()" [disabled]="loadingPending()">
              @if (loadingPending()) { Refreshing... } @else { Refresh }
            </button>
          </div>

          @if (loadingPending()) {
            <div class="loading-state">Loading pending registrations...</div>
          } @else if (pendingUsers().length === 0) {
            <div class="empty-state">No pending registrations.</div>
          } @else {
            <div class="table-wrapper">
              <table class="users-table">
                <thead>
                  <tr>
                    <th>Username</th>
                    <th>Email</th>
                    <th>Name</th>
                    <th>Organization</th>
                    <th>Justification</th>
                    <th>Requested</th>
                    <th>Classification</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  @for (user of pendingUsers(); track user.id) {
                    <tr>
                      <td class="monospace">{{ user.username }}</td>
                      <td>{{ user.email }}</td>
                      <td>{{ user.firstName }} {{ user.lastName }}</td>
                      <td>{{ user.organization }}</td>
                      <td class="justification-cell">{{ user.justification }}</td>
                      <td class="monospace date-cell">{{ user.registrationDate | date:'short' }}</td>
                      <td>
                        <select
                          class="classification-select"
                          (change)="setPendingClassification(user.id, $any($event.target).value)"
                          [disabled]="actionInProgress() === user.id">
                          @for (opt of classificationOptions; track opt.value) {
                            <option [value]="opt.value" [selected]="opt.value === getPendingClassification(user.id)">{{ opt.label }}</option>
                          }
                        </select>
                      </td>
                      <td class="actions-cell">
                        <button
                          class="action-btn approve-btn"
                          (click)="approve(user.id)"
                          [disabled]="actionInProgress() === user.id">
                          @if (actionInProgress() === user.id && actionType() === 'approve') {
                            Approving...
                          } @else {
                            Approve
                          }
                        </button>
                        <button
                          class="action-btn reject-btn"
                          (click)="reject(user.id)"
                          [disabled]="actionInProgress() === user.id">
                          @if (actionInProgress() === user.id && actionType() === 'reject') {
                            Rejecting...
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

        <!-- Active Users -->
        <div class="panel">
          <div class="panel-header">
            <h2 class="panel-title">Active Users</h2>
            <button class="refresh-btn" (click)="loadActiveUsers()" [disabled]="loadingActive()">
              @if (loadingActive()) { Refreshing... } @else { Refresh }
            </button>
          </div>

          @if (loadingActive()) {
            <div class="loading-state">Loading active users...</div>
          } @else if (activeUsers().length === 0) {
            <div class="empty-state">No active users.</div>
          } @else {
            <div class="table-wrapper">
              <table class="users-table">
                <thead>
                  <tr>
                    <th>Username</th>
                    <th>Email</th>
                    <th>Name</th>
                    <th>Classification</th>
                  </tr>
                </thead>
                <tbody>
                  @for (user of activeUsers(); track user.id) {
                    <tr>
                      <td class="monospace">{{ user.username }}</td>
                      <td>{{ user.email }}</td>
                      <td>{{ user.firstName }} {{ user.lastName }}</td>
                      <td>
                        <select
                          class="classification-select"
                          (change)="updateClassification(user.id, $any($event.target).value)"
                          [disabled]="classificationUpdating() === user.id">
                          @for (opt of classificationOptions; track opt.value) {
                            <option [value]="opt.value" [selected]="opt.value === (user.classificationLevel ?? 'classification-u')">{{ opt.label }}</option>
                          }
                        </select>
                        @if (classificationUpdating() === user.id) {
                          <span class="updating-indicator">Updating...</span>
                        }
                      </td>
                    </tr>
                  }
                </tbody>
              </table>
            </div>
          }
        </div>
      }
    </div>
  `,
  styles: [`
    :host {
      display: block;
      padding: 32px;
      min-height: 100%;
    }

    .settings-page {
      max-width: 1200px;
      margin: 0 auto;
    }

    .settings-header {
      margin-bottom: 24px;
    }

    .settings-title {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 24px;
      font-weight: 600;
      letter-spacing: 2px;
      color: rgba(255, 255, 255, 0.9);
      margin: 0;
      text-transform: uppercase;
    }

    .tabs {
      display: flex;
      gap: 4px;
      margin-bottom: 24px;
      border-bottom: 1px solid rgba(59, 130, 246, 0.15);
      padding-bottom: 0;
    }

    .tab-btn {
      padding: 10px 20px;
      background: transparent;
      border: none;
      border-bottom: 2px solid transparent;
      color: rgba(255, 255, 255, 0.5);
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 13px;
      font-weight: 600;
      letter-spacing: 1px;
      text-transform: uppercase;
      cursor: pointer;
      transition: color 0.2s ease, border-color 0.2s ease;
      margin-bottom: -1px;
    }

    .tab-btn:hover {
      color: rgba(255, 255, 255, 0.7);
    }

    .tab-btn.active {
      color: rgba(59, 130, 246, 0.9);
      border-bottom-color: rgba(59, 130, 246, 0.9);
    }

    .panel {
      background: rgba(8, 16, 38, 0.92);
      border: 1px solid rgba(59, 130, 246, 0.2);
      border-radius: 10px;
      padding: 24px;
      box-shadow: 0 0 40px rgba(59, 130, 246, 0.06);
      margin-bottom: 20px;
    }

    .panel-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 20px;
      padding-bottom: 16px;
      border-bottom: 1px solid rgba(59, 130, 246, 0.15);
    }

    .panel-title {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 16px;
      font-weight: 600;
      letter-spacing: 2px;
      color: rgba(255, 255, 255, 0.9);
      margin: 0;
      text-transform: uppercase;
    }

    /* Profile tab */
    .profile-section {
      display: flex;
      gap: 32px;
      align-items: flex-start;
    }

    .profile-avatar {
      width: 64px;
      height: 64px;
      border-radius: 50%;
      background: rgba(59, 130, 246, 0.15);
      border: 1px solid rgba(59, 130, 246, 0.3);
      color: rgba(59, 130, 246, 0.9);
      display: flex;
      align-items: center;
      justify-content: center;
      font-weight: 700;
      font-size: 24px;
      flex-shrink: 0;
    }

    .profile-info {
      flex: 1;
      display: flex;
      flex-direction: column;
      gap: 16px;
    }

    .info-row {
      display: flex;
      align-items: center;
      gap: 16px;
    }

    .info-label {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 10px;
      font-weight: 600;
      letter-spacing: 1.5px;
      text-transform: uppercase;
      color: rgba(255, 255, 255, 0.4);
      width: 120px;
      flex-shrink: 0;
    }

    .info-value {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 14px;
      color: rgba(255, 255, 255, 0.85);
    }

    .info-value.classification {
      font-family: 'JetBrains Mono', 'Fira Code', 'Courier New', monospace;
      font-weight: 600;
      color: #f59e0b;
    }

    .role-badges {
      display: flex;
      gap: 6px;
      flex-wrap: wrap;
    }

    .role-badge {
      padding: 3px 10px;
      background: rgba(59, 130, 246, 0.12);
      border: 1px solid rgba(59, 130, 246, 0.25);
      border-radius: 4px;
      font-family: 'JetBrains Mono', 'Fira Code', 'Courier New', monospace;
      font-size: 11px;
      color: rgba(59, 130, 246, 0.9);
    }

    /* Shared table styles */
    .refresh-btn {
      padding: 8px 16px;
      background: rgba(59, 130, 246, 0.15);
      border: 1px solid rgba(59, 130, 246, 0.3);
      border-radius: 6px;
      color: rgba(59, 130, 246, 0.9);
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 12px;
      font-weight: 600;
      letter-spacing: 1px;
      cursor: pointer;
      transition: background 0.2s ease, border-color 0.2s ease;
    }

    .refresh-btn:hover:not(:disabled) {
      background: rgba(59, 130, 246, 0.25);
      border-color: rgba(59, 130, 246, 0.5);
    }

    .refresh-btn:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }

    .toast {
      padding: 12px 16px;
      border-radius: 6px;
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 13px;
      margin-bottom: 16px;
    }

    .toast-error {
      background: rgba(239, 68, 68, 0.15);
      border: 1px solid rgba(239, 68, 68, 0.3);
      color: #f87171;
    }

    .toast-success {
      background: rgba(34, 197, 94, 0.15);
      border: 1px solid rgba(34, 197, 94, 0.3);
      color: #4ade80;
    }

    .loading-state, .empty-state {
      padding: 48px;
      text-align: center;
      color: rgba(255, 255, 255, 0.4);
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 14px;
    }

    .table-wrapper {
      overflow-x: auto;
    }

    .users-table {
      width: 100%;
      border-collapse: collapse;
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 13px;
    }

    .users-table th {
      text-align: left;
      padding: 10px 12px;
      font-size: 10px;
      font-weight: 600;
      letter-spacing: 1.5px;
      text-transform: uppercase;
      color: rgba(255, 255, 255, 0.4);
      border-bottom: 1px solid rgba(59, 130, 246, 0.15);
    }

    .users-table td {
      padding: 12px 12px;
      color: rgba(255, 255, 255, 0.8);
      border-bottom: 1px solid rgba(255, 255, 255, 0.05);
      vertical-align: middle;
    }

    .users-table tbody tr:hover td {
      background: rgba(59, 130, 246, 0.05);
    }

    .monospace {
      font-family: 'JetBrains Mono', 'Fira Code', 'Courier New', monospace;
      font-size: 12px;
      color: rgba(59, 130, 246, 0.9);
    }

    .date-cell {
      white-space: nowrap;
      font-size: 11px;
    }

    .justification-cell {
      max-width: 200px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
      color: rgba(255, 255, 255, 0.5);
      font-size: 12px;
    }

    .actions-cell {
      white-space: nowrap;
    }

    .action-btn {
      padding: 6px 14px;
      border-radius: 5px;
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 11px;
      font-weight: 600;
      letter-spacing: 1px;
      cursor: pointer;
      transition: background 0.2s ease, border-color 0.2s ease;
      margin-right: 6px;
    }

    .action-btn:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }

    .approve-btn {
      background: rgba(34, 197, 94, 0.15);
      border: 1px solid rgba(34, 197, 94, 0.35);
      color: #4ade80;
    }

    .approve-btn:hover:not(:disabled) {
      background: rgba(34, 197, 94, 0.25);
      border-color: rgba(34, 197, 94, 0.6);
    }

    .reject-btn {
      background: rgba(239, 68, 68, 0.15);
      border: 1px solid rgba(239, 68, 68, 0.35);
      color: #f87171;
    }

    .reject-btn:hover:not(:disabled) {
      background: rgba(239, 68, 68, 0.25);
      border-color: rgba(239, 68, 68, 0.6);
    }

    .classification-select {
      padding: 6px 10px;
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid rgba(59, 130, 246, 0.2);
      border-radius: 5px;
      color: rgba(255, 255, 255, 0.9);
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 12px;
      cursor: pointer;
      outline: none;
    }

    .classification-select:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }

    .classification-select option {
      background: #0e1e3d;
      color: rgba(255, 255, 255, 0.9);
    }

    .updating-indicator {
      margin-left: 8px;
      font-size: 11px;
      color: rgba(59, 130, 246, 0.7);
    }
  `],
})
export class SettingsComponent {
  private readonly http = inject(HttpClient);
  private readonly authService = inject(AuthService);

  readonly profile = toSignal(this.authService.userProfile$);
  readonly isAdmin = computed(() => this.profile()?.roles?.includes('sentinel-admin') ?? false);
  readonly sentinelRoles = computed(() =>
    (this.profile()?.roles ?? [])
      .filter(r => r.startsWith('sentinel-'))
      .map(r => r.replace('sentinel-', '')),
  );

  activeTab = signal<'profile' | 'management'>('profile');

  // User Management state
  pendingUsers = signal<PendingUser[]>([]);
  activeUsers = signal<ActiveUser[]>([]);
  loadingPending = signal(false);
  loadingActive = signal(false);
  actionInProgress = signal<string | null>(null);
  actionType = signal<'approve' | 'reject' | null>(null);
  classificationUpdating = signal<string | null>(null);
  errorMessage = signal('');
  successMessage = signal('');

  private pendingClassifications = signal<Record<string, string>>({});

  readonly classificationOptions = [
    { value: 'classification-u', label: 'UNCLASSIFIED' },
    { value: 'classification-s', label: 'SECRET' },
    { value: 'classification-ts', label: 'TOP SECRET' },
  ];

  private managementLoaded = false;

  switchTab(tab: 'profile' | 'management'): void {
    this.activeTab.set(tab);
    if (tab === 'management' && !this.managementLoaded) {
      this.managementLoaded = true;
      this.loadPendingUsers();
      this.loadActiveUsers();
    }
  }

  getPendingClassification(userId: string): string {
    return this.pendingClassifications()[userId] ?? 'classification-u';
  }

  setPendingClassification(userId: string, value: string): void {
    this.pendingClassifications.update(m => ({ ...m, [userId]: value }));
  }

  loadPendingUsers(): void {
    this.loadingPending.set(true);
    this.errorMessage.set('');
    this.http.get<PendingUser[]>('/api/v1/auth/pending-registrations').subscribe({
      next: (users) => { this.pendingUsers.set(users); this.loadingPending.set(false); },
      error: (err) => {
        this.errorMessage.set(err.error?.message ?? 'Failed to load pending registrations.');
        this.loadingPending.set(false);
      },
    });
  }

  loadActiveUsers(): void {
    this.loadingActive.set(true);
    this.http.get<ActiveUser[]>('/api/v1/auth/users').subscribe({
      next: (users) => { this.activeUsers.set(users); this.loadingActive.set(false); },
      error: (err) => {
        this.errorMessage.set(err.error?.message ?? 'Failed to load active users.');
        this.loadingActive.set(false);
      },
    });
  }

  approve(userId: string): void {
    const classificationLevel = this.getPendingClassification(userId);
    this.actionInProgress.set(userId);
    this.actionType.set('approve');
    this.errorMessage.set('');

    this.http.post(`/api/v1/auth/approve-registration/${userId}`, { classificationLevel }).subscribe({
      next: () => {
        this.pendingUsers.set(this.pendingUsers().filter(u => u.id !== userId));
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.successMessage.set('User approved successfully.');
        this.autoClearSuccess();
        this.loadActiveUsers();
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message ?? 'Failed to approve registration.');
        this.actionInProgress.set(null);
        this.actionType.set(null);
      },
    });
  }

  reject(userId: string): void {
    this.actionInProgress.set(userId);
    this.actionType.set('reject');
    this.errorMessage.set('');

    this.http.post(`/api/v1/auth/reject-registration/${userId}`, {}).subscribe({
      next: () => {
        this.pendingUsers.set(this.pendingUsers().filter(u => u.id !== userId));
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.successMessage.set('User rejected successfully.');
        this.autoClearSuccess();
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message ?? 'Failed to reject registration.');
        this.actionInProgress.set(null);
        this.actionType.set(null);
      },
    });
  }

  updateClassification(userId: string, classificationLevel: string): void {
    this.classificationUpdating.set(userId);
    this.http.put(`/api/v1/auth/users/${userId}/classification`, { classificationLevel }).subscribe({
      next: () => {
        this.activeUsers.set(this.activeUsers().map(u =>
          u.id === userId ? { ...u, classificationLevel } : u,
        ));
        this.classificationUpdating.set(null);
        this.successMessage.set('Classification updated.');
        this.autoClearSuccess();
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message ?? 'Failed to update classification.');
        this.classificationUpdating.set(null);
      },
    });
  }

  private autoClearSuccess(): void {
    setTimeout(() => this.successMessage.set(''), 3000);
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test web --testFile=apps/web/src/app/features/settings/settings.component.spec.ts`

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/settings/settings.component.ts apps/web/src/app/features/settings/settings.component.spec.ts
git commit -m "feat(settings): create SettingsComponent with Profile and User Management tabs"
```

---

### Task 6: Update routes and AppComponent

**Files:**
- Modify: `apps/web/src/app/app.routes.ts`
- Modify: `apps/web/src/app/app.component.ts`
- Delete: `apps/web/src/app/features/admin/pending-users/pending-users.component.ts`
- Delete: `apps/web/src/app/features/admin/pending-users/pending-users.component.spec.ts`

**Context:** Wire up the new SettingsComponent:
1. Add `/settings` route (lazy-loaded, `authGuard`)
2. Remove `/admin/users` route
3. Change gear icon from `(click)="toggleSettings()"` to `routerLink="/settings"` with `routerLinkActive="active"`
4. Remove the admin-only user management sidebar button (the users icon) — functionality now lives in Settings
5. Update `isPageRoute` in `classifyRoute` to also match `/settings`
6. Remove the `toggleSettings()` method
7. Delete old `pending-users.component.ts` and its spec

- [ ] **Step 1: Update `app.routes.ts`**

Replace the `admin/users` route with `settings`:

```typescript
// Remove this:
{
  path: 'admin/users',
  canActivate: [roleGuard('sentinel-admin')],
  loadComponent: () =>
    import('./features/admin/pending-users/pending-users.component').then(
      (m) => m.PendingUsersComponent,
    ),
},

// Add these (before the wildcard route):
{
  path: 'settings',
  canActivate: [authGuard],
  loadComponent: () =>
    import('./features/settings/settings.component').then(
      (m) => m.SettingsComponent,
    ),
},
{
  path: 'admin/users',
  redirectTo: 'settings',
},,
```

- [ ] **Step 2: Update `app.component.ts`**

In the sidebar footer, remove the admin users button and change the gear icon:

```typescript
// Remove the entire @if (isAdmin()) block with the users icon button

// Change the settings button from:
<button
  class="sidebar-btn"
  (click)="toggleSettings()"
  title="Settings">

// To:
<button
  class="sidebar-btn"
  routerLink="/settings"
  routerLinkActive="active"
  title="Settings">
```

Update `classifyRoute` to include `/settings`:
```typescript
const classifyRoute = (url: string) => {
  const path = url.replace(/\?.*$/, '');
  this.isFullScreenRoute.set(['/login', '/register'].includes(path));
  this.isPageRoute.set(path.startsWith('/admin') || path.startsWith('/settings'));
};
```

Remove the `toggleSettings()` method.

- [ ] **Step 3: Delete old files**

```bash
rm apps/web/src/app/features/admin/pending-users/pending-users.component.ts
rm apps/web/src/app/features/admin/pending-users/pending-users.component.spec.ts
```

- [ ] **Step 4: Run tests**

Run: `npx nx test web`

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/app.routes.ts apps/web/src/app/app.component.ts
git rm apps/web/src/app/features/admin/pending-users/pending-users.component.ts apps/web/src/app/features/admin/pending-users/pending-users.component.spec.ts
git commit -m "feat(settings): wire up /settings route, remove /admin/users and old pending-users component"
```

---

### Task 7: Integration verification

- [ ] **Step 1: Run all tests**

```bash
npx nx test api-gateway
npx nx test web
```

- [ ] **Step 2: Run builds**

```bash
npx nx build api-gateway
npx nx build web
```

- [ ] **Step 3: Verify all pass with no errors**
