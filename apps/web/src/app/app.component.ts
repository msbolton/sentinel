import { Component, OnInit, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { CommonModule } from '@angular/common';
import { Router, RouterOutlet, RouterLink, RouterLinkActive } from '@angular/router';
import { WebSocketService, ConnectionStatus } from './core/services/websocket.service';
import { AuthService, UserProfile } from './core/services/auth.service';
import { AlertService } from './core/services/alert.service';
import { MapComponent } from './features/map/map.component';

@Component({
  selector: 'app-root',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, RouterOutlet, RouterLink, RouterLinkActive, MapComponent],
  template: `
    <!-- Sidebar Navigation -->
    <nav class="sidebar">
      <div class="sidebar-logo" title="SENTINEL">S</div>

      <div class="sidebar-nav">
        <button
          class="sidebar-btn"
          routerLink="/map"
          routerLinkActive="active"
          title="Map View">
          &#127758;
        </button>
        <button
          class="sidebar-btn"
          routerLink="/search"
          routerLinkActive="active"
          title="Search Entities">
          &#128269;
        </button>
        <button
          class="sidebar-btn"
          routerLink="/alerts"
          routerLinkActive="active"
          title="Alerts">
          &#128276;
          @if (unacknowledgedAlertCount() > 0) {
            <span class="badge">{{ unacknowledgedAlertCount() > 99 ? '99+' : unacknowledgedAlertCount() }}</span>
          }
        </button>
        <button
          class="sidebar-btn"
          routerLink="/link-graph"
          routerLinkActive="active"
          title="Link Analysis">
          &#128304;
        </button>
        <button
          class="sidebar-btn"
          routerLink="/timeline"
          routerLinkActive="active"
          title="Timeline">
          &#9202;
        </button>
      </div>

      <div class="sidebar-footer">
        <button
          class="sidebar-btn"
          (click)="toggleSettings()"
          title="Settings">
          &#9881;
        </button>
        <button
          class="sidebar-btn"
          (click)="handleAuth()"
          [title]="userProfile()?.username ?? 'Login'">
          &#128100;
        </button>
      </div>
    </nav>

    <!-- Main Content Area -->
    <main class="main-content">
      <!-- Map is always rendered in the background -->
      <app-map class="map-background"></app-map>

      <!-- Feature panels render on top of the map -->
      <div class="panel-overlay">
        <router-outlet></router-outlet>
      </div>
    </main>

    <!-- Status Bar -->
    <footer class="status-bar">
      <div class="status-item" [ngClass]="connectionStatus()">
        <span class="status-dot"></span>
        <span>{{ connectionStatus() | titlecase }}</span>
      </div>
      <span class="status-item">
        <span class="font-mono">{{ entityCount() }} entities</span>
      </span>
      <div class="status-spacer"></div>
      <span class="status-item font-mono">
        {{ userProfile()?.classificationLevel ?? 'UNCLASSIFIED' }}
      </span>
      <span class="status-item font-mono">
        {{ userProfile()?.username ?? '--' }}
      </span>
      <span class="status-item font-mono">{{ currentTime() }}</span>
    </footer>
  `,
  styles: [`
    :host {
      display: block;
      width: 100%;
      height: 100%;
      overflow: hidden;
    }

    .main-content {
      position: fixed;
      top: 0;
      left: var(--sidebar-width);
      right: 0;
      bottom: var(--status-bar-height);
      overflow: hidden;
    }

    .map-background {
      position: absolute;
      top: 0;
      left: 0;
      width: 100%;
      height: 100%;
      z-index: 1;
    }

    .panel-overlay {
      position: absolute;
      top: 0;
      right: 0;
      height: 100%;
      z-index: 100;
      pointer-events: none;

      > * {
        pointer-events: all;
      }
    }
  `],
})
export class AppComponent implements OnInit {
  connectionStatus = signal<ConnectionStatus>('disconnected');
  entityCount = signal<number>(0);
  unacknowledgedAlertCount = signal<number>(0);
  userProfile = signal<UserProfile | null>(null);
  currentTime = signal<string>('');

  private timeInterval: ReturnType<typeof setInterval> | null = null;

  constructor(
    private readonly router: Router,
    private readonly wsService: WebSocketService,
    private readonly authService: AuthService,
    private readonly alertService: AlertService,
  ) {}

  async ngOnInit(): Promise<void> {
    // Initialize auth
    await this.authService.init();

    // Store auth service reference for interceptor
    (window as any).__sentinelAuthService = this.authService;

    // Connect WebSocket
    this.wsService.connect();

    // Subscribe to connection status
    this.wsService.connectionStatus$.subscribe((status) => {
      this.connectionStatus.set(status);
    });

    // Subscribe to user profile
    this.authService.userProfile$.subscribe((profile) => {
      this.userProfile.set(profile);
    });

    // Subscribe to alert count
    this.alertService.unacknowledgedCount$.subscribe((count) => {
      this.unacknowledgedAlertCount.set(count);
    });

    // Update clock
    this.updateTime();
    this.timeInterval = setInterval(() => this.updateTime(), 1000);
  }

  toggleSettings(): void {
    // Future: open settings panel
    console.log('Settings panel - not yet implemented');
  }

  handleAuth(): void {
    if (this.authService.isAuthenticated()) {
      this.authService.logout();
    } else {
      this.authService.login();
    }
  }

  private updateTime(): void {
    const now = new Date();
    this.currentTime.set(
      now.toISOString().replace('T', ' ').substring(0, 19) + 'Z',
    );
  }
}
