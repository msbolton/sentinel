import { Component, OnInit, DestroyRef, HostListener, inject, signal, computed, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { CommonModule } from '@angular/common';
import { Router, RouterOutlet, RouterLink, RouterLinkActive, NavigationEnd } from '@angular/router';
import { filter } from 'rxjs';
import { WebSocketService, ConnectionStatus } from './core/services/websocket.service';
import { AuthService, UserProfile } from './core/services/auth.service';
import { AlertService } from './core/services/alert.service';
import { AnalyticsService } from './core/services/analytics.service';
import { ThemeService } from './core/services/theme.service';
import { NAV_GROUPS, NavItem, routeForShortcut } from './core/services/nav-rail.service';
import { IconComponent } from './shared/components/icon.component';
import { HasRoleDirective } from './shared/directives/has-role.directive';
import { MapComponent } from './features/map/map.component';
import { WelcomeCardComponent } from './features/demo/welcome-card.component';
import { SessionEndedComponent } from './features/demo/session-ended.component';

@Component({
  selector: 'app-root',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, RouterOutlet, RouterLink, RouterLinkActive, MapComponent, HasRoleDirective, IconComponent, WelcomeCardComponent, SessionEndedComponent],
  template: `
    @if (isFullScreenRoute()) {
      <!-- Login page renders full-screen, no app shell -->
      <router-outlet></router-outlet>
    } @else {
      <!-- Tool rail: icons only, centred on the left edge; labels show in a flyout on hover or focus -->
      <nav class="rail" aria-label="Primary">
        @for (group of navGroups; track group.label) {
          <div class="rail-group">
            @for (item of group.items; track item.path) {
              @if (item.roles) {
                <ng-container *appHasRole="item.roles">
                  <ng-container *ngTemplateOutlet="railItem; context: { $implicit: item }" />
                </ng-container>
              } @else {
                <ng-container *ngTemplateOutlet="railItem; context: { $implicit: item }" />
              }
            }
          </div>
          <div class="rail-gap" aria-hidden="true"></div>
        }
        <div class="rail-group">
          <ng-container *ngTemplateOutlet="railItem; context: { $implicit: settingsItem }" />
        </div>
      </nav>

      <ng-template #railItem let-item>
        <a
          class="rail-item"
          [routerLink]="item.path"
          routerLinkActive="active"
          [attr.aria-label]="item.label"
          [attr.data-key]="item.key || null">
          <app-icon [name]="item.icon" [size]="18" />
          @if (badgeCount(item); as n) {
            <span class="badge" [class.badge-findings]="item.badge === 'findings'">{{ n > 99 ? '99+' : n }}</span>
          }
          <span class="rail-flyout" role="tooltip">
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

      <!-- Main Content Area: fills the window; the rail floats over it -->
      <main class="main-content">
        @if (isPageRoute()) {
          <!-- Full-page routes (admin) render directly without map -->
          <div class="page-content">
            <router-outlet></router-outlet>
          </div>
        } @else {
          <!-- Map is always rendered in the background -->
          <app-map class="map-background"></app-map>

          <!-- Feature panels render on top of the map -->
          <div class="panel-overlay">
            <router-outlet></router-outlet>
          </div>
        }
      </main>

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

    .main-content {
      position: fixed;
      inset: 0;
      overflow: hidden;
    }

    .page-content {
      position: absolute;
      inset: 0;
      overflow-y: auto;
      padding-left: 48px;
      background: var(--bg-primary);
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
  private readonly destroyRef = inject(DestroyRef);
  private readonly themeService = inject(ThemeService);

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
}
