import { Component, OnInit, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { CommonModule, DatePipe } from '@angular/common';
import { HttpClient } from '@angular/common/http';

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

@Component({
  selector: 'app-pending-users',
  standalone: true,
  imports: [CommonModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="pending-users-panel">
      <div class="panel-header">
        <h2 class="panel-title">Pending Registrations</h2>
        <button class="refresh-btn" (click)="loadUsers()" [disabled]="loadingList()">
          @if (loadingList()) {
            Refreshing...
          } @else {
            Refresh
          }
        </button>
      </div>

      @if (errorMessage()) {
        <div class="toast toast-error">{{ errorMessage() }}</div>
      }

      @if (successMessage()) {
        <div class="toast toast-success">{{ successMessage() }}</div>
      }

      @if (loadingList()) {
        <div class="loading-state">Loading pending registrations...</div>
      } @else if (users().length === 0) {
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
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              @for (user of users(); track user.id) {
                <tr>
                  <td class="monospace">{{ user.username }}</td>
                  <td>{{ user.email }}</td>
                  <td>{{ user.firstName }} {{ user.lastName }}</td>
                  <td>{{ user.organization }}</td>
                  <td class="justification-cell">{{ user.justification }}</td>
                  <td class="monospace date-cell">{{ user.registrationDate | date:'short' }}</td>
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
  `,
  styles: [`
    :host {
      display: block;
      padding: 24px;
      background: #060e1f;
      min-height: 100vh;
    }

    .pending-users-panel {
      max-width: 1200px;
      margin: 0 auto;
      background: rgba(8, 16, 38, 0.92);
      border: 1px solid rgba(59, 130, 246, 0.2);
      border-radius: 10px;
      padding: 24px;
      box-shadow: 0 0 40px rgba(59, 130, 246, 0.06);
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
      font-size: 18px;
      font-weight: 600;
      letter-spacing: 2px;
      color: rgba(255, 255, 255, 0.9);
      margin: 0;
      text-transform: uppercase;
    }

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

    .loading-state {
      padding: 48px;
      text-align: center;
      color: rgba(255, 255, 255, 0.4);
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 14px;
    }

    .empty-state {
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

    this.http.get<PendingUser[]>('/api/v1/auth/pending-registrations').subscribe({
      next: (users) => {
        this.users.set(users);
        this.loadingList.set(false);
      },
      error: (err) => {
        this.errorMessage.set(err.error?.message ?? 'Failed to load pending registrations.');
        this.loadingList.set(false);
      },
    });
  }

  approve(userId: string): void {
    this.actionInProgress.set(userId);
    this.actionType.set('approve');
    this.errorMessage.set('');

    this.http.post(`/api/v1/auth/approve-registration/${userId}`, {}).subscribe({
      next: () => {
        this.users.set(this.users().filter(u => u.id !== userId));
        this.actionInProgress.set(null);
        this.actionType.set(null);
        this.successMessage.set('User approved successfully.');
        this.autoClearSuccess();
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
        this.users.set(this.users().filter(u => u.id !== userId));
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

  autoClearSuccess(): void {
    setTimeout(() => {
      this.successMessage.set('');
    }, 3000);
  }
}
