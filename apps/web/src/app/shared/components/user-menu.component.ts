import {
  Component,
  ChangeDetectionStrategy,
  signal,
  inject,
  HostListener,
  ElementRef,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { AuthService, UserProfile } from '../../core/services/auth.service';
import { toSignal } from '@angular/core/rxjs-interop';

@Component({
  selector: 'app-user-menu',
  standalone: true,
  imports: [CommonModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="user-menu-container" [class.expanded]="expanded()">
      <button class="user-btn" (click)="toggle()" [title]="profile()?.username ?? 'Login'">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
             stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
          <circle cx="12" cy="7" r="4"/>
        </svg>
        @if (isAuthenticated()) {
          <span class="auth-indicator"></span>
        }
      </button>

      @if (expanded()) {
        <div class="user-panel">
          @if (isAuthenticated()) {
            <div class="user-info">
              <div class="user-avatar">
                {{ (profile()?.username ?? '?')[0] | uppercase }}
              </div>
              <div class="user-details">
                <span class="user-name">{{ profile()?.username }}</span>
                <span class="user-email">{{ profile()?.email ?? '' }}</span>
              </div>
            </div>
            <div class="user-meta">
              <div class="meta-row">
                <span class="meta-label">CLEARANCE</span>
                <span class="meta-value classification">{{ profile()?.classificationLevel }}</span>
              </div>
              <div class="meta-row">
                <span class="meta-label">ROLES</span>
                <span class="meta-value">{{ formatRoles(profile()?.roles ?? []) }}</span>
              </div>
            </div>
            <button class="menu-action logout" (click)="logout()">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                   stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
                <polyline points="16 17 21 12 16 7"/>
                <line x1="21" y1="12" x2="9" y2="12"/>
              </svg>
              Logout
            </button>
          } @else {
            <button class="menu-action login" (click)="login()">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                   stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/>
                <polyline points="10 17 15 12 10 7"/>
                <line x1="15" y1="12" x2="3" y2="12"/>
              </svg>
              Login with Keycloak
            </button>
          }
        </div>
      }
    </div>
  `,
  styles: [`
    :host {
      display: block;
      position: relative;
    }

    .user-btn {
      position: relative;
      display: flex;
      align-items: center;
      justify-content: center;
      width: 40px;
      height: 40px;
      border-radius: var(--radius-md);
      background: transparent;
      color: var(--text-muted);
      transition: all var(--transition-fast);
      cursor: pointer;

      &:hover {
        color: var(--text-primary);
        background: color-mix(in srgb, var(--text-muted) 10%, transparent);
      }
    }

    .auth-indicator {
      position: absolute;
      top: 6px;
      right: 6px;
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: var(--accent-green, #22c55e);
      border: 2px solid var(--bg-sidebar, #0a0e17);
    }

    .user-panel {
      position: absolute;
      bottom: calc(100% + 8px);
      left: calc(100% + 8px);
      width: 260px;
      background: var(--bg-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--border-color);
      border-radius: var(--radius-lg);
      padding: 12px;
      animation: floatIn 200ms cubic-bezier(0.16, 1, 0.3, 1);
      z-index: 1000;
    }

    .user-info {
      display: flex;
      align-items: center;
      gap: 12px;
      padding-bottom: 12px;
      border-bottom: 1px solid var(--border-color);
    }

    .user-avatar {
      width: 36px;
      height: 36px;
      border-radius: 50%;
      background: color-mix(in srgb, var(--accent-blue) 20%, transparent);
      color: var(--accent-blue);
      display: flex;
      align-items: center;
      justify-content: center;
      font-weight: 700;
      font-size: 0.875rem;
      flex-shrink: 0;
    }

    .user-details {
      display: flex;
      flex-direction: column;
      min-width: 0;
    }

    .user-name {
      font-weight: 600;
      font-size: 0.875rem;
      color: var(--text-primary);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .user-email {
      font-size: 0.75rem;
      color: var(--text-muted);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .user-meta {
      padding: 10px 0;
      border-bottom: 1px solid var(--border-color);
    }

    .meta-row {
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 4px 0;
    }

    .meta-label {
      font-family: var(--font-mono);
      font-size: 0.65rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--text-muted);
    }

    .meta-value {
      font-size: 0.75rem;
      color: var(--text-secondary);
      text-align: right;
    }

    .classification {
      font-family: var(--font-mono);
      font-weight: 600;
      color: var(--accent-amber, #f59e0b);
    }

    .menu-action {
      width: 100%;
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 8px 10px;
      margin-top: 8px;
      background: transparent;
      border-radius: var(--radius-sm);
      font-size: 0.8rem;
      color: var(--text-secondary);
      cursor: pointer;
      transition: all var(--transition-fast);

      &:hover {
        background: color-mix(in srgb, var(--text-muted) 10%, transparent);
        color: var(--text-primary);
      }

      &.logout:hover {
        color: var(--accent-red, #ef4444);
      }

      &.login {
        background: color-mix(in srgb, var(--accent-blue) 15%, transparent);
        color: var(--accent-blue);

        &:hover {
          background: color-mix(in srgb, var(--accent-blue) 25%, transparent);
        }
      }
    }

    @keyframes floatIn {
      from {
        opacity: 0;
        transform: translateY(4px);
      }
      to {
        opacity: 1;
        transform: translateY(0);
      }
    }
  `],
})
export class UserMenuComponent {
  private readonly authService = inject(AuthService);
  private readonly elementRef = inject(ElementRef);

  readonly expanded = signal(false);
  readonly profile = toSignal(this.authService.userProfile$);
  readonly isAuthenticated = toSignal(this.authService.isAuthenticated$, { initialValue: false });

  toggle(): void {
    this.expanded.update((v) => !v);
  }

  login(): void {
    this.authService.login();
    this.expanded.set(false);
  }

  logout(): void {
    this.authService.logout();
    this.expanded.set(false);
  }

  formatRoles(roles: string[]): string {
    return roles
      .filter((r) => r.startsWith('sentinel-'))
      .map((r) => r.replace('sentinel-', ''))
      .join(', ') || 'none';
  }

  @HostListener('document:click', ['$event'])
  onDocumentClick(event: MouseEvent): void {
    if (!this.elementRef.nativeElement.contains(event.target)) {
      this.expanded.set(false);
    }
  }
}
