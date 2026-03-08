import { Component, OnInit, OnDestroy, DestroyRef, inject, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { CommonModule } from '@angular/common';
import { Router, RouterOutlet, RouterLink, RouterLinkActive } from '@angular/router';
import { WebSocketService, ConnectionStatus } from './core/services/websocket.service';
import { AuthService, UserProfile } from './core/services/auth.service';
import { AlertService } from './core/services/alert.service';
import { ThemeService } from './core/services/theme.service';
import { ThemePickerComponent } from './shared/components/theme-picker.component';
import { DataFeedsComponent } from './shared/components/data-feeds.component';
import { MapComponent } from './features/map/map.component';

@Component({
  selector: 'app-root',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, RouterOutlet, RouterLink, RouterLinkActive, MapComponent, ThemePickerComponent, DataFeedsComponent],
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
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10"/>
            <path d="M2 12h20"/>
            <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
          </svg>
        </button>
        <button
          class="sidebar-btn"
          routerLink="/search"
          routerLinkActive="active"
          title="Search Entities">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="8"/>
            <path d="M21 21l-4.35-4.35"/>
          </svg>
        </button>
        <button
          class="sidebar-btn"
          routerLink="/alerts"
          routerLinkActive="active"
          title="Alerts">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/>
            <path d="M13.73 21a2 2 0 0 1-3.46 0"/>
          </svg>
          @if (unacknowledgedAlertCount() > 0) {
            <span class="badge">{{ unacknowledgedAlertCount() > 99 ? '99+' : unacknowledgedAlertCount() }}</span>
          }
        </button>
        <button
          class="sidebar-btn"
          routerLink="/link-graph"
          routerLinkActive="active"
          title="Link Analysis">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="5" cy="6" r="3"/>
            <circle cx="19" cy="6" r="3"/>
            <circle cx="12" cy="19" r="3"/>
            <path d="M7.5 8l4 8.5"/>
            <path d="M16.5 8l-4 8.5"/>
            <path d="M8 6h8"/>
          </svg>
        </button>
        <button
          class="sidebar-btn"
          routerLink="/timeline"
          routerLinkActive="active"
          title="Timeline">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10"/>
            <polyline points="12 6 12 12 16 14"/>
          </svg>
        </button>
        <button
          class="sidebar-btn"
          routerLink="/locations"
          routerLinkActive="active"
          title="Locations">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0 1 18 0z"/>
            <circle cx="12" cy="10" r="3"/>
          </svg>
        </button>
      </div>

      <div class="sidebar-footer">
        <button
          class="sidebar-btn"
          (click)="toggleSettings()"
          title="Settings">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
          </svg>
        </button>
        <button
          class="sidebar-btn"
          (click)="handleAuth()"
          [title]="userProfile()?.username ?? 'Login'">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
            <circle cx="12" cy="7" r="4"/>
          </svg>
        </button>
      </div>
    </nav>

    <!-- Main Content Area -->
    <main class="main-content">
      <!-- Map is always rendered in the background -->
      <app-map class="map-background"></app-map>

      <!-- Floating pills -->
      <div class="floating-pills">
        <app-data-feeds></app-data-feeds>
        <app-theme-picker></app-theme-picker>
      </div>

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

    .floating-pills {
      position: absolute;
      left: 16px;
      bottom: 16px;
      z-index: 50;
      display: flex;
      flex-direction: column;
      align-items: flex-start;
      gap: 8px;
      max-height: calc(100% - 32px);
      overflow-y: auto;
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
export class AppComponent implements OnInit, OnDestroy {
  connectionStatus = signal<ConnectionStatus>('disconnected');
  entityCount = signal<number>(0);
  unacknowledgedAlertCount = signal<number>(0);
  userProfile = signal<UserProfile | null>(null);
  currentTime = signal<string>('');
  private readonly destroyRef = inject(DestroyRef);
  private readonly themeService = inject(ThemeService);
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
    this.wsService.connectionStatus$.pipe(
      takeUntilDestroyed(this.destroyRef),
    ).subscribe((status) => {
      this.connectionStatus.set(status);
    });

    // Subscribe to user profile
    this.authService.userProfile$.pipe(
      takeUntilDestroyed(this.destroyRef),
    ).subscribe((profile) => {
      this.userProfile.set(profile);
    });

    // Subscribe to alert count
    this.alertService.unacknowledgedCount$.pipe(
      takeUntilDestroyed(this.destroyRef),
    ).subscribe((count) => {
      this.unacknowledgedAlertCount.set(count);
    });

    // Update clock
    this.updateTime();
    this.timeInterval = setInterval(() => this.updateTime(), 1000);
  }

  ngOnDestroy(): void {
    if (this.timeInterval) {
      clearInterval(this.timeInterval);
      this.timeInterval = null;
    }
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
