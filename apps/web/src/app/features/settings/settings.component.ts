import { Component, ChangeDetectionStrategy, inject, signal, computed } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { HttpClient } from '@angular/common/http';
import { DatePipe, UpperCasePipe } from '@angular/common';
import { AuthService, UserProfile } from '../../core/services/auth.service';
import { FederationService } from '../../core/services/federation.service';

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
        @if (isAdmin()) {
          <button
            class="tab-btn"
            [class.active]="activeTab() === 'federation'"
            (click)="switchTab('federation')"
          >Federation</button>
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

      @if (activeTab() === 'federation') {
        <div class="tab-content federation-tab">
          @if (federationError()) {
            <div class="toast error">{{ federationError() }}</div>
          }
          @if (federationSuccess()) {
            <div class="toast success">{{ federationSuccess() }}</div>
          }

          <div class="settings-section">
            <h2>Instance Configuration</h2>
            @if (federationConfig(); as config) {
              <div class="config-form">
                <div class="form-row">
                  <label>Instance ID</label>
                  <span class="monospace">{{ config.instanceId }}</span>
                </div>
                <div class="form-row">
                  <label>Display Name</label>
                  <input
                    type="text"
                    [value]="config.displayName"
                    (change)="updateFederationDisplayName($event)"
                    class="input-field"
                  />
                </div>
                <div class="form-row">
                  <label>Classification</label>
                  <span class="classification-badge">{{ config.classificationLevel | uppercase }}</span>
                </div>
                <div class="form-row">
                  <label>Federation</label>
                  <label class="toggle-label">
                    <input
                      type="checkbox"
                      [checked]="config.federationEnabled"
                      (change)="toggleFederation($event)"
                    />
                    {{ config.federationEnabled ? 'Enabled' : 'Disabled' }}
                  </label>
                </div>
              </div>
            } @else if (loadingFederation()) {
              <div class="loading">Loading federation config...</div>
            }
          </div>

          <div class="settings-section">
            <h2>Seed Peers</h2>
            <div class="add-peer-form">
              <input
                type="text"
                placeholder="ws://hostname:3100"
                [value]="addPeerUrl()"
                (input)="addPeerUrl.set($any($event.target).value)"
                class="input-field"
              />
              <input
                type="text"
                placeholder="Display Name"
                [value]="addPeerName()"
                (input)="addPeerName.set($any($event.target).value)"
                class="input-field input-sm"
              />
              <button class="btn-primary btn-sm" (click)="addSeedPeer()">Add Peer</button>
            </div>

            <div class="peer-table">
              @for (peer of federationPeers(); track peer.instanceId) {
                <div class="peer-row-admin">
                  <span class="peer-status-dot" [class]="'status-' + peer.status"></span>
                  <span class="peer-name">{{ peer.displayName }}</span>
                  <span class="peer-url monospace">{{ peer.url }}</span>
                  <span class="peer-classification">{{ peer.classificationLevel | uppercase }}</span>
                  <button class="btn-danger btn-xs" (click)="removePeer(peer.instanceId)">Remove</button>
                </div>
              } @empty {
                <div class="empty-state">No peers configured</div>
              }
            </div>
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

    .federation-tab .settings-section { margin-bottom: 24px; }
    .config-form { display: flex; flex-direction: column; gap: 12px; }
    .form-row { display: flex; align-items: center; gap: 12px; }
    .form-row label { width: 140px; color: #888; font-size: 13px; }
    .input-field {
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid #333;
      border-radius: 4px;
      padding: 6px 10px;
      color: #ccc;
      font-size: 13px;
    }
    .input-sm { width: 160px; }
    .toggle-label { display: flex; align-items: center; gap: 8px; cursor: pointer; color: #ccc; }
    .add-peer-form { display: flex; gap: 8px; margin-bottom: 12px; align-items: center; }
    .peer-table { display: flex; flex-direction: column; gap: 6px; }
    .peer-row-admin {
      display: flex; align-items: center; gap: 10px;
      padding: 8px 12px; background: rgba(255, 255, 255, 0.03); border-radius: 6px;
    }
    .peer-status-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
    .status-connected { background: #22c55e; }
    .status-stale { background: #eab308; }
    .status-disconnected { background: #ef4444; }
    .peer-name { color: #ccc; font-weight: 500; }
    .peer-url { color: #666; font-size: 11px; flex: 1; }
    .peer-classification { color: #888; font-size: 11px; text-transform: uppercase; }
    .btn-primary {
      background: #3b82f6; color: white; border: none;
      border-radius: 4px; padding: 6px 14px; cursor: pointer; font-size: 12px;
    }
    .btn-primary:hover { background: #2563eb; }
    .btn-danger {
      background: transparent; color: #ef4444; border: 1px solid #ef4444;
      border-radius: 4px; padding: 3px 10px; cursor: pointer; font-size: 11px;
    }
    .btn-danger:hover { background: rgba(239, 68, 68, 0.1); }
    .btn-xs { padding: 2px 8px; font-size: 10px; }
    .empty-state { color: #666; font-style: italic; padding: 12px 0; }
  `],
})
export class SettingsComponent {
  private readonly authService = inject(AuthService);
  private readonly http = inject(HttpClient);
  private readonly federationService = inject(FederationService);

  readonly profile = toSignal(this.authService.userProfile$);

  readonly activeTab = signal<'profile' | 'management' | 'federation'>('profile');

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

  federationConfig = signal<any>(null);
  federationPeers = signal<any[]>([]);
  loadingFederation = signal(false);
  federationError = signal('');
  federationSuccess = signal('');
  addPeerUrl = signal('');
  addPeerName = signal('');

  private readonly pendingClassifications = signal<Record<string, string>>({});
  private managementLoaded = false;
  private federationLoaded = false;

  readonly classificationOptions = [
    { value: 'classification-u', label: 'UNCLASSIFIED' },
    { value: 'classification-s', label: 'SECRET' },
    { value: 'classification-ts', label: 'TOP SECRET' },
  ] as const;

  switchTab(tab: 'profile' | 'management' | 'federation'): void {
    this.activeTab.set(tab);
    if (tab === 'management' && !this.managementLoaded) {
      this.managementLoaded = true;
      this.loadPendingUsers();
      this.loadActiveUsers();
    }
    if (tab === 'federation' && !this.federationLoaded) {
      this.federationLoaded = true;
      this.loadFederationData();
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
        this.autoClearSuccess();
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
        this.autoClearSuccess();
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message || 'Failed to reject user');
        this.actionInProgress.set(null);
        this.actionType.set(null);
      },
    });
  }

  private autoClearSuccess(): void {
    setTimeout(() => this.successMessage.set(''), 3000);
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
        this.autoClearSuccess();
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message || 'Failed to update classification');
        this.classificationUpdating.set(null);
      },
    });
  }

  loadFederationData(): void {
    this.loadingFederation.set(true);
    this.federationError.set('');
    let pending = 2;
    const done = () => { if (--pending === 0) this.loadingFederation.set(false); };
    this.federationService.getConfig().subscribe({
      next: (config) => { this.federationConfig.set(config); done(); },
      error: (err) => { this.federationError.set(err.error?.message ?? 'Failed to load config'); done(); },
    });
    this.federationService.getPeers().subscribe({
      next: (peers) => { this.federationPeers.set(peers); done(); },
      error: (err) => { this.federationError.set(err.error?.message ?? 'Failed to load peers'); done(); },
    });
  }

  updateFederationDisplayName(event: Event): void {
    const name = (event.target as HTMLInputElement).value;
    this.federationService.updateConfig({ displayName: name }).subscribe({
      next: (config) => {
        this.federationConfig.set(config);
        this.federationSuccess.set('Display name updated');
        setTimeout(() => this.federationSuccess.set(''), 3000);
      },
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to update'),
    });
  }

  toggleFederation(event: Event): void {
    const enabled = (event.target as HTMLInputElement).checked;
    this.federationService.updateConfig({ federationEnabled: enabled }).subscribe({
      next: (config) => {
        this.federationConfig.set(config);
        this.federationSuccess.set(`Federation ${enabled ? 'enabled' : 'disabled'}`);
        setTimeout(() => this.federationSuccess.set(''), 3000);
      },
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to toggle'),
    });
  }

  addSeedPeer(): void {
    const url = this.addPeerUrl().trim();
    const name = this.addPeerName().trim();
    if (!url || !name) return;
    this.federationService.addPeer(url, name).subscribe({
      next: () => {
        this.addPeerUrl.set('');
        this.addPeerName.set('');
        this.federationSuccess.set('Peer added');
        setTimeout(() => this.federationSuccess.set(''), 3000);
        this.loadFederationData();
      },
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to add peer'),
    });
  }

  removePeer(instanceId: string): void {
    this.federationService.removePeer(instanceId).subscribe({
      next: () => {
        this.federationSuccess.set('Peer removed');
        setTimeout(() => this.federationSuccess.set(''), 3000);
        this.loadFederationData();
      },
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to remove peer'),
    });
  }
}
