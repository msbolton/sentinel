import { Component, OnInit, OnDestroy, signal, ChangeDetectionStrategy, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { IconComponent, IconName } from '../../shared/components/icon.component';
import { Subscription } from 'rxjs';
import { Alert, AlertSeverity, AlertType } from '../../shared/models/alert.model';
import { AlertService } from '../../core/services/alert.service';
import { EntityService } from '../../core/services/entity.service';
import { SelectionService } from '../../core/services/selection.service';

@Component({
  selector: 'app-alerts',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule, IconComponent],
  templateUrl: './alerts.component.html',
  styleUrls: ['./alerts.component.scss'],
})
export class AlertsComponent implements OnInit, OnDestroy {
  alerts = signal<Alert[]>([]);
  loading = signal<boolean>(false);
  selectedSeverity = signal<AlertSeverity | null>(null);
  selectedType = signal<AlertType | null>(null);
  showAcknowledged = signal<boolean>(false);
  unacknowledgedCount = signal<number>(0);

  severities = Object.values(AlertSeverity);
  alertTypes = Object.values(AlertType);

  private subscriptions = new Subscription();
  private readonly selection = inject(SelectionService);
  private readonly entities = inject(EntityService);

  constructor(private readonly alertService: AlertService) {}

  ngOnInit(): void {
    this.loadAlerts();
    // The service's first count ran at app start, which may have been before login.
    this.alertService.refreshUnacknowledgedCount();

    // Subscribe to real-time alerts
    const alertSub = this.alertService.alertStream$.subscribe((alert) => {
      this.mergeAlert(alert);
    });
    this.subscriptions.add(alertSub);

    // Track unacknowledged count
    const countSub = this.alertService.unacknowledgedCount$.subscribe((count) => {
      this.unacknowledgedCount.set(count);
    });
    this.subscriptions.add(countSub);
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
  }

  loadAlerts(): void {
    this.loading.set(true);
    this.alertService
      .getAlerts({
        severity: this.selectedSeverity() ?? undefined,
        alertType: this.selectedType() ?? undefined,
        acknowledged: this.showAcknowledged() ? undefined : false,
        pageSize: 100,
      })
      .subscribe({
        next: (response) => {
          this.alerts.set(response.data);
          this.loading.set(false);
        },
        error: () => {
          this.loading.set(false);
        },
      });
  }

  acknowledgeAlert(alert: Alert, event: Event): void {
    event.stopPropagation();
    this.alertService.acknowledgeAlert(alert.id).subscribe({
      next: (updated) => {
        const current = this.alerts();
        const index = current.findIndex((a) => a.id === alert.id);
        if (index >= 0) {
          const next = [...current];
          next[index] = { ...current[index], ...updated, acknowledged: true };
          this.alerts.set(next);
        }
      },
    });
  }

  /** Replace an existing row in place (keeping fields a sparse update omits), else add it on top. */
  private mergeAlert(incoming: Alert): void {
    const current = this.alerts();
    const index = current.findIndex((a) => a.id === incoming.id);
    if (index < 0) {
      this.alerts.set([incoming, ...current]);
      return;
    }
    const next = [...current];
    next[index] = { ...current[index], ...incoming };
    this.alerts.set(next);
  }

  filterBySeverity(severity: AlertSeverity | null): void {
    this.selectedSeverity.set(severity);
    this.loadAlerts();
  }

  filterByType(type: AlertType | null): void {
    this.selectedType.set(type);
    this.loadAlerts();
  }

  toggleShowAcknowledged(): void {
    this.showAcknowledged.set(!this.showAcknowledged());
    this.loadAlerts();
  }

  /**
   * Selects the alerted track: from the map's cache when it is loaded, else
   * fetched from the API; when there is no track to show, flies to where the
   * alert fired.
   */
  flyToAlert(alert: Alert): void {
    const flyToPosition = () => {
      if (alert.position) this.selection.flyTo({ lat: alert.position.latitude, lon: alert.position.longitude });
    };
    if (!alert.entityId) {
      flyToPosition();
      return;
    }
    if (this.selection.selectById(alert.entityId)) return;
    const sub = this.entities.getEntity(alert.entityId).subscribe({
      next: (entity) => this.selection.select(entity),
      error: flyToPosition,
    });
    this.subscriptions.add(sub);
  }

  getSeverityIcon(severity: AlertSeverity): IconName {
    switch (severity) {
      case AlertSeverity.CRITICAL: return 'alert-octagon';
      case AlertSeverity.HIGH: return 'alert-triangle';
      case AlertSeverity.MEDIUM: return 'alert-circle';
      case AlertSeverity.LOW: return 'info';
      case AlertSeverity.INFO: return 'info';
      default: return 'alert-circle';
    }
  }

  getSeverityClass(severity: AlertSeverity): string {
    return severity.toLowerCase();
  }

  getTimeAgo(dateStr: string): string {
    const date = new Date(dateStr);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMins / 60);
    const diffDays = Math.floor(diffHours / 24);

    if (diffMins < 1) return 'just now';
    if (diffMins < 60) return `${diffMins}m ago`;
    if (diffHours < 24) return `${diffHours}h ago`;
    return `${diffDays}d ago`;
  }
}
