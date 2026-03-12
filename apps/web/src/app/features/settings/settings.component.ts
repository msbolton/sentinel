import { Component, ChangeDetectionStrategy, inject, signal, computed } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { HttpClient } from '@angular/common/http';
import { DatePipe, UpperCasePipe } from '@angular/common';
import { AuthService, UserProfile } from '../../core/services/auth.service';

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

export interface ActiveUser {
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
      <h1 class="page-title">Settings</h1>

      <div class="tab-bar">
        <button
          class="tab-btn"
          [class.active]="activeTab() === 'profile'"
          (click)="switchTab('profile')"
        >Profile</button>
        @if (isAdmin()) {
          <button
            class="tab-btn"
            [class.active]="activeTab() === 'management'"
            (click)="switchTab('management')"
          >User Management</button>
        }
      </div>

      @if (activeTab() === 'profile') {
        <div class="tab-content profile-tab">
          @if (profile(); as p) {
            <div class="profile-card">
              <div class="avatar">{{ p.username.charAt(0).toUpperCase() }}</div>
              <div class="profile-info">
                <div class="info-row">
                  <span class="label">Username</span>
                  <span class="value monospace">{{ p.username }}</span>
                </div>
                <div class="info-row">
                  <span class="label">Email</span>
                  <span class="value monospace">{{ p.email || 'N/A' }}</span>
                </div>
                <div class="info-row">
                  <span class="label">Classification</span>
                  <span class="value classification-badge">{{ p.classificationLevel | uppercase }}</span>
                </div>
                <div class="info-row">
                  <span class="label">Roles</span>
                  <span class="value roles">
                    @for (role of sentinelRoles(); track role) {
                      <span class="role-badge">{{ role }}</span>
                    }
                  </span>
                </div>
              </div>
            </div>
          }
        </div>
      }

      @if (activeTab() === 'management') {
        <div class="tab-content management-tab">
          @if (errorMessage()) {
            <div class="toast error">{{ errorMessage() }}</div>
          }
          @if (successMessage()) {
            <div class="toast success">{{ successMessage() }}</div>
          }

          <!-- Pending Registrations -->
          <div class="panel">
            <div class="panel-header">
              <h2>Pending Registrations</h2>
              <button class="refresh-btn" (click)="loadPendingUsers()">Refresh</button>
            </div>
            @if (loadingPending()) {
              <div class="loading">Loading pending registrations...</div>
            } @else if (pendingUsers().length === 0) {
              <div class="empty">No pending registrations</div>
            } @else {
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Username</th>
                    <th>Email</th>
                    <th>Name</th>
                    <th>Organization</th>
                    <th>Justification</th>
                    <th>Date</th>
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
                      <td>{{ user.justification }}</td>
                      <td>{{ user.registrationDate | date:'short' }}</td>
                      <td>
                        <select (change)="setPendingClassification(user.id, $any($event.target).value)">
                          @for (opt of classificationOptions; track opt.value) {
                            <option
                              [value]="opt.value"
                              [selected]="opt.value === getPendingClassification(user.id)"
                            >{{ opt.label }}</option>
                          }
                        </select>
                      </td>
                      <td class="actions">
                        <button
                          class="btn approve"
                          [disabled]="actionInProgress() === user.id"
                          (click)="approve(user.id)"
                        >
                          {{ actionInProgress() === user.id && actionType() === 'approve' ? 'Approving...' : 'Approve' }}
                        </button>
                        <button
                          class="btn reject"
                          [disabled]="actionInProgress() === user.id"
                          (click)="reject(user.id)"
                        >
                          {{ actionInProgress() === user.id && actionType() === 'reject' ? 'Rejecting...' : 'Reject' }}
                        </button>
                      </td>
                    </tr>
                  }
                </tbody>
              </table>
            }
          </div>

          <!-- Active Users -->
          <div class="panel">
            <div class="panel-header">
              <h2>Active Users</h2>
              <button class="refresh-btn" (click)="loadActiveUsers()">Refresh</button>
            </div>
            @if (loadingActive()) {
              <div class="loading">Loading active users...</div>
            } @else if (activeUsers().length === 0) {
              <div class="empty">No active users</div>
            } @else {
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Username</th>
                    <th>Email</th>
                    <th>Name</th>
                    <th>Roles</th>
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
                        @for (role of user.roles; track role) {
                          <span class="role-badge small">{{ role }}</span>
                        }
                      </td>
                      <td>
                        <select (change)="updateClassification(user.id, $any($event.target).value)">
                          @for (opt of classificationOptions; track opt.value) {
                            <option
                              [value]="opt.value"
                              [selected]="opt.value === (user.classificationLevel || '')"
                            >{{ opt.label }}</option>
                          }
                        </select>
                        @if (classificationUpdating() === user.id) {
                          <span class="updating">Saving...</span>
                        }
                      </td>
                    </tr>
                  }
                </tbody>
              </table>
            }
          </div>
        </div>
      }
    </div>
  `,
  styles: [`
    :host {
      display: block;
      padding: 24px;
      color: #e0e0e0;
      font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif;
    }

    .page-title {
      font-size: 24px;
      font-weight: 600;
      margin: 0 0 20px 0;
      color: #ffffff;
    }

    .tab-bar {
      display: flex;
      gap: 4px;
      margin-bottom: 24px;
      border-bottom: 1px solid #2a2a3e;
      padding-bottom: 0;
    }

    .tab-btn {
      padding: 10px 20px;
      background: transparent;
      border: none;
      border-bottom: 2px solid transparent;
      color: #8888aa;
      font-size: 14px;
      cursor: pointer;
      transition: all 0.2s;
    }

    .tab-btn:hover {
      color: #b0b0cc;
    }

    .tab-btn.active {
      color: #4a9eff;
      border-bottom-color: #4a9eff;
    }

    .profile-card {
      display: flex;
      gap: 24px;
      background: #1a1a2e;
      border: 1px solid #2a2a3e;
      border-radius: 8px;
      padding: 24px;
      max-width: 600px;
    }

    .avatar {
      width: 64px;
      height: 64px;
      border-radius: 50%;
      background: #4a9eff;
      color: #ffffff;
      display: flex;
      align-items: center;
      justify-content: center;
      font-size: 28px;
      font-weight: 600;
      flex-shrink: 0;
    }

    .profile-info {
      flex: 1;
      display: flex;
      flex-direction: column;
      gap: 12px;
    }

    .info-row {
      display: flex;
      flex-direction: column;
      gap: 4px;
    }

    .label {
      font-size: 11px;
      text-transform: uppercase;
      color: #6666aa;
      letter-spacing: 0.5px;
    }

    .value {
      font-size: 14px;
      color: #e0e0e0;
    }

    .monospace {
      font-family: 'JetBrains Mono', 'Fira Code', monospace;
    }

    .classification-badge {
      display: inline-block;
      padding: 2px 8px;
      background: #4a9eff22;
      border: 1px solid #4a9eff44;
      border-radius: 4px;
      font-size: 12px;
      font-weight: 600;
      color: #4a9eff;
    }

    .roles {
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
    }

    .role-badge {
      padding: 3px 10px;
      background: #2a2a3e;
      border: 1px solid #3a3a5e;
      border-radius: 12px;
      font-size: 12px;
      color: #b0b0cc;
    }

    .role-badge.small {
      padding: 2px 6px;
      font-size: 11px;
    }

    .panel {
      background: #1a1a2e;
      border: 1px solid #2a2a3e;
      border-radius: 8px;
      padding: 20px;
      margin-bottom: 20px;
    }

    .panel-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 16px;
    }

    .panel-header h2 {
      margin: 0;
      font-size: 16px;
      font-weight: 600;
      color: #ffffff;
    }

    .refresh-btn {
      padding: 6px 12px;
      background: #2a2a3e;
      border: 1px solid #3a3a5e;
      border-radius: 4px;
      color: #b0b0cc;
      cursor: pointer;
      font-size: 12px;
    }

    .refresh-btn:hover {
      background: #3a3a5e;
    }

    .loading, .empty {
      padding: 20px;
      text-align: center;
      color: #6666aa;
      font-size: 13px;
    }

    .toast {
      padding: 10px 16px;
      border-radius: 6px;
      margin-bottom: 16px;
      font-size: 13px;
    }

    .toast.error {
      background: #ff4a4a22;
      border: 1px solid #ff4a4a44;
      color: #ff6b6b;
    }

    .toast.success {
      background: #4aff4a22;
      border: 1px solid #4aff4a44;
      color: #6bff6b;
    }

    .data-table {
      width: 100%;
      border-collapse: collapse;
      font-size: 13px;
    }

    .data-table th {
      text-align: left;
      padding: 8px 12px;
      border-bottom: 1px solid #2a2a3e;
      color: #6666aa;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.5px;
    }

    .data-table td {
      padding: 10px 12px;
      border-bottom: 1px solid #1e1e32;
      color: #c0c0dd;
    }

    .data-table select {
      padding: 4px 8px;
      background: #0e0e1a;
      border: 1px solid #2a2a3e;
      border-radius: 4px;
      color: #e0e0e0;
      font-size: 12px;
    }

    .actions {
      display: flex;
      gap: 6px;
    }

    .btn {
      padding: 5px 12px;
      border: none;
      border-radius: 4px;
      font-size: 12px;
      cursor: pointer;
      transition: background 0.2s;
    }

    .btn:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }

    .btn.approve {
      background: #2a7a2a;
      color: #ffffff;
    }

    .btn.approve:hover:not(:disabled) {
      background: #3a9a3a;
    }

    .btn.reject {
      background: #7a2a2a;
      color: #ffffff;
    }

    .btn.reject:hover:not(:disabled) {
      background: #9a3a3a;
    }

    .updating {
      margin-left: 8px;
      font-size: 11px;
      color: #4a9eff;
    }
  `],
})
export class SettingsComponent {
  private readonly authService = inject(AuthService);
  private readonly http = inject(HttpClient);

  readonly profile = toSignal(this.authService.userProfile$);

  readonly activeTab = signal<'profile' | 'management'>('profile');

  readonly isAdmin = computed(() => {
    const p = this.profile();
    return p?.roles?.includes('sentinel-admin') ?? false;
  });

  readonly sentinelRoles = computed(() => {
    const p = this.profile();
    if (!p) return [];
    return p.roles.filter((r: string) => r.startsWith('sentinel-'));
  });

  readonly pendingUsers = signal<PendingUser[]>([]);
  readonly activeUsers = signal<ActiveUser[]>([]);
  readonly loadingPending = signal(false);
  readonly loadingActive = signal(false);
  readonly actionInProgress = signal<string | null>(null);
  readonly actionType = signal<'approve' | 'reject' | null>(null);
  readonly classificationUpdating = signal<string | null>(null);
  readonly errorMessage = signal('');
  readonly successMessage = signal('');

  private readonly pendingClassifications = signal<Record<string, string>>({});
  private managementLoaded = false;

  readonly classificationOptions = [
    { value: 'classification-u', label: 'UNCLASSIFIED' },
    { value: 'classification-c', label: 'CONFIDENTIAL' },
    { value: 'classification-s', label: 'SECRET' },
    { value: 'classification-ts', label: 'TOP SECRET' },
  ] as const;

  switchTab(tab: 'profile' | 'management'): void {
    this.activeTab.set(tab);
    if (tab === 'management' && !this.managementLoaded) {
      this.managementLoaded = true;
      this.loadPendingUsers();
      this.loadActiveUsers();
    }
  }

  getPendingClassification(userId: string): string {
    return this.pendingClassifications()[userId] || 'classification-u';
  }

  setPendingClassification(userId: string, value: string): void {
    this.pendingClassifications.update(current => ({ ...current, [userId]: value }));
  }

  loadPendingUsers(): void {
    this.loadingPending.set(true);
    this.errorMessage.set('');
    this.http.get<PendingUser[]>('/api/v1/auth/pending-registrations').subscribe({
      next: (users) => {
        this.pendingUsers.set(users);
        this.loadingPending.set(false);
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message || 'Failed to load pending registrations');
        this.loadingPending.set(false);
      },
    });
  }

  loadActiveUsers(): void {
    this.loadingActive.set(true);
    this.http.get<ActiveUser[]>('/api/v1/auth/users').subscribe({
      next: (users) => {
        this.activeUsers.set(users);
        this.loadingActive.set(false);
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message || 'Failed to load active users');
        this.loadingActive.set(false);
      },
    });
  }

  approve(userId: string): void {
    this.actionInProgress.set(userId);
    this.actionType.set('approve');
    const classificationLevel = this.getPendingClassification(userId);

    this.http.post(`/api/v1/auth/approve-registration/${userId}`, { classificationLevel }).subscribe({
      next: () => {
        this.pendingUsers.update(users => users.filter(u => u.id !== userId));
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.successMessage.set(`User approved successfully`);
        this.loadActiveUsers();
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message || 'Failed to approve user');
        this.actionInProgress.set(null);
        this.actionType.set(null);
      },
    });
  }

  reject(userId: string): void {
    this.actionInProgress.set(userId);
    this.actionType.set('reject');

    this.http.post(`/api/v1/auth/reject-registration/${userId}`, {}).subscribe({
      next: () => {
        this.pendingUsers.update(users => users.filter(u => u.id !== userId));
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.successMessage.set(`User registration rejected`);
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message || 'Failed to reject user');
        this.actionInProgress.set(null);
        this.actionType.set(null);
      },
    });
  }

  updateClassification(userId: string, level: string): void {
    this.classificationUpdating.set(userId);

    this.http.put(`/api/v1/auth/users/${userId}/classification`, { classificationLevel: level }).subscribe({
      next: () => {
        this.activeUsers.update(users =>
          users.map(u => u.id === userId ? { ...u, classificationLevel: level } : u)
        );
        this.classificationUpdating.set(null);
        this.successMessage.set(`Classification updated successfully`);
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message || 'Failed to update classification');
        this.classificationUpdating.set(null);
      },
    });
  }
}
