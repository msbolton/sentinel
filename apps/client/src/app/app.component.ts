import { Component, OnInit, OnDestroy, DestroyRef, HostListener, inject, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { CommonModule } from '@angular/common';
import { Router, RouterOutlet, RouterLink, RouterLinkActive, NavigationEnd } from '@angular/router';
import { filter } from 'rxjs';
import { WebSocketService, ConnectionStatus } from './core/services/websocket.service';
import { AuthService, UserProfile } from './core/services/auth.service';
import { AlertService } from './core/services/alert.service';
import { AnalyticsService } from './core/services/analytics.service';
import { ThemeService } from './core/services/theme.service';
import { NAV_GROUPS, NavItem, NavRailService, routeForShortcut } from './core/services/nav-rail.service';
import { IconComponent } from './shared/components/icon.component';
import { ThemePickerComponent } from './shared/components/theme-picker.component';
import { LayersPillComponent } from './shared/components/layers-pill.component';
import { UserMenuComponent } from './shared/components/user-menu.component';
import { HasRoleDirective } from './shared/directives/has-role.directive';
import { MapComponent } from './features/map/map.component';
import { DemoBadgeComponent } from './features/demo/demo-badge.component';
import { WelcomeCardComponent } from './features/demo/welcome-card.component';
import { SessionEndedComponent } from './features/demo/session-ended.component';

@Component({
  selector: 'app-root',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, RouterOutlet, RouterLink, RouterLinkActive, MapComponent, ThemePickerComponent, LayersPillComponent, UserMenuComponent, HasRoleDirective, IconComponent, DemoBadgeComponent, WelcomeCardComponent, SessionEndedComponent],
  template: `
    @if (isFullScreenRoute()) {
      <!-- Login page renders full-screen, no app shell -->
      <router-outlet></router-outlet>
    } @else {
      <!-- Navigation rail: labelled when expanded, flyout labels when collapsed -->
      <nav class="sidebar" [class.collapsed]="!rail.expanded()" aria-label="Primary">
        <div class="sidebar-brand">
          <img class="sidebar-logo" src="assets/brand/sentinel-icon-64.png" alt="" aria-hidden="true" width="28" height="28" />
          <span class="sidebar-wordmark">SENTINEL</span>
        </div>
        <!-- Only a demo guest sees this; it sits under the brand so it shows whether the rail is open or not. -->
        @if (isGuest()) {
          <app-demo-badge class="demo-badge" [compact]="!rail.expanded()"></app-demo-badge>
        }

        <div class="sidebar-nav">
          @for (group of navGroups; track group.label) {
            <div class="sidebar-group">
              <div class="sidebar-group-label">{{ group.label }}</div>
              @for (item of group.items; track item.path) {
                @if (item.roles) {
                  <ng-container *appHasRole="item.roles">
                    <ng-container *ngTemplateOutlet="navItem; context: { $implicit: item }" />
                  </ng-container>
                } @else {
                  <ng-container *ngTemplateOutlet="navItem; context: { $implicit: item }" />
                }
              }
            </div>
          }
        </div>

        <div class="sidebar-footer">
          <button
            type="button"
            class="sidebar-collapse"
            (click)="rail.toggle()"
            [attr.aria-expanded]="rail.expanded()"
            [attr.aria-label]="rail.expanded() ? 'Collapse navigation' : 'Expand navigation'"
            [title]="rail.expanded() ? 'Collapse navigation' : 'Expand navigation'">
            <app-icon [name]="rail.expanded() ? 'chevron-left' : 'chevron-right'" />
            <span class="sidebar-item-label">Collapse</span>
          </button>
          <ng-container *ngTemplateOutlet="navItem; context: { $implicit: settingsItem }" />
          <app-user-menu [expanded]="rail.expanded()"></app-user-menu>
        </div>
      </nav>

      <ng-template #navItem let-item>
        <a
          class="sidebar-item"
          [routerLink]="item.path"
          routerLinkActive="active"
          [attr.aria-label]="item.label"
          [attr.data-key]="item.key || null">
          <app-icon [name]="item.icon" [size]="20" />
          <span class="sidebar-item-label">{{ item.label }}</span>
          @if (badgeCount(item); as n) {
            <span class="badge" [class.badge-findings]="item.badge === 'findings'">{{ n > 99 ? '99+' : n }}</span>
          } @else if (item.key) {
            <kbd class="sidebar-key">{{ item.key | uppercase }}</kbd>
          }
          <span class="sidebar-flyout" role="tooltip">
            {{ item.label }}
            @if (badgeCount(item); as n) {
              <span class="flyout-count">{{ n }} open</span>
            }
            @if (item.key) {
              <kbd>{{ item.key | uppercase }}</kbd>
            }
          </span>
        </a>
      </ng-template>

      <!-- Main Content Area -->
      <main class="main-content">
        @if (isPageRoute()) {
          <!-- Full-page routes (admin) render directly without map -->
          <div class="page-content">
            <router-outlet></router-outlet>
          </div>
        } @else {
          <!-- Map is always rendered in the background -->
          <app-map class="map-background"></app-map>

          <!-- Floating pills -->
          <div class="floating-pills">
            <app-layers-pill></app-layers-pill>
            <app-theme-picker></app-theme-picker>
          </div>

          <!-- Feature panels render on top of the map -->
          <div class="panel-overlay">
            <router-outlet></router-outlet>
          </div>
        }
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

      <!-- The public demo's cards: each shows only for a guest (or an ended guest session). -->
      <app-welcome-card></app-welcome-card>
      <app-session-ended></app-session-ended>
    }
  `,
  styles: [`
    :host {
      display: block;
      width: 100%;
      height: 100%;
      overflow: hidden;
    }

    .demo-badge {
      display: flex;
      justify-content: center;
      flex-shrink: 0;
    }

    .main-content {
      position: fixed;
      top: 0;
      left: var(--sidebar-width);
      right: 0;
      bottom: var(--status-bar-height);
      overflow: hidden;
    }

    .page-content {
      position: absolute;
      inset: 0;
      overflow-y: auto;
      background: #060e1f;
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
  readonly rail = inject(NavRailService);
  readonly navGroups = NAV_GROUPS;
  readonly settingsItem: NavItem = { path: '/settings', label: 'Settings', icon: 'settings', key: '' };
  isFullScreenRoute = signal<boolean>(false);
  isPageRoute = signal<boolean>(false);
  connectionStatus = signal<ConnectionStatus>('disconnected');
  entityCount = signal<number>(0);
  unacknowledgedAlertCount = signal<number>(0);
  openFindingCount = inject(AnalyticsService).openCount;
  userProfile = signal<UserProfile | null>(null);
  isAdmin = computed(() => this.userProfile()?.roles?.includes('sentinel-admin') ?? false);
  isGuest = computed(() => this.userProfile()?.roles?.includes('sentinel-guest') ?? false);
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
    // Track route type for layout switching
    const classifyRoute = (url: string) => {
      const path = url.replace(/\?.*$/, '');
      this.isFullScreenRoute.set(['/login', '/register', '/demo'].includes(path));
      this.isPageRoute.set(path.startsWith('/admin') || path.startsWith('/settings') || path.startsWith('/connections') || path.startsWith('/feeds'));
    };
    classifyRoute(this.router.url);
    this.router.events.pipe(
      filter((e): e is NavigationEnd => e instanceof NavigationEnd),
      takeUntilDestroyed(this.destroyRef),
    ).subscribe((e) => classifyRoute(e.urlAfterRedirects));

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

  /** The live count an item carries on its badge, or 0 for none. */
  badgeCount(item: NavItem): number {
    switch (item.badge) {
      case 'alerts': return this.unacknowledgedAlertCount();
      case 'findings': return this.openFindingCount() ?? 0;
      default: return 0;
    }
  }

  /** Single-key navigation shortcuts, listed on each rail item. */
  @HostListener('document:keydown', ['$event'])
  onKeydown(event: KeyboardEvent): void {
    if (this.isFullScreenRoute()) return;
    const path = routeForShortcut(event);
    if (path) {
      event.preventDefault();
      this.router.navigateByUrl(path);
    }
  }

  ngOnDestroy(): void {
    if (this.timeInterval) {
      clearInterval(this.timeInterval);
      this.timeInterval = null;
    }
  }

  private updateTime(): void {
    const now = new Date();
    this.currentTime.set(
      now.toISOString().replace('T', ' ').substring(0, 19) + 'Z',
    );
  }
}
