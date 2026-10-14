import {
  Directive,
  Input,
  TemplateRef,
  ViewContainerRef,
  OnDestroy,
  inject,
} from '@angular/core';
import { Subscription } from 'rxjs';
import { AuthService } from '../../core/services/auth.service';

/**
 * Structural directive that conditionally renders content based on user roles.
 *
 * Usage:
 *   <button *appHasRole="'admin'">Delete</button>
 *   <button *appHasRole="['analyst', 'admin']">Edit</button>
 */
@Directive({
  selector: '[appHasRole]',
  standalone: true,
})
export class HasRoleDirective implements OnDestroy {
  private readonly authService = inject(AuthService);
  private readonly templateRef = inject(TemplateRef<unknown>);
  private readonly viewContainer = inject(ViewContainerRef);
  private subscription: Subscription | null = null;
  private hasView = false;

  @Input()
  set appHasRole(roles: string | string[]) {
    const requiredRoles = Array.isArray(roles) ? roles : [roles];

    this.subscription?.unsubscribe();
    this.subscription = this.authService.userProfile$.subscribe((profile) => {
      const userRoles = profile?.roles ?? [];
      const hasRole = requiredRoles.some((role) => userRoles.includes(role));

      if (hasRole && !this.hasView) {
        this.viewContainer.createEmbeddedView(this.templateRef);
        this.hasView = true;
      } else if (!hasRole && this.hasView) {
        this.viewContainer.clear();
        this.hasView = false;
      }
    });
  }

  ngOnDestroy(): void {
    this.subscription?.unsubscribe();
  }
}
