import { Component, OnInit, OnDestroy, signal, ChangeDetectionStrategy } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Subscription } from 'rxjs';
import { Alert, AlertSeverity, AlertType } from '../../shared/models/alert.model';
import { AlertService } from '../../core/services/alert.service';

@Component({
  selector: 'app-alerts',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule],
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

  constructor(private readonly alertService: AlertService) {}

  ngOnInit(): void {
    this.loadAlerts();

    // Subscribe to real-time alerts
    const alertSub = this.alertService.alertStream$.subscribe((alert) => {
      const current = this.alerts();
      this.alerts.set([alert, ...current]);
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
        limit: 100,
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
        const index = current.findIndex((a) => a.id === updated.id);
        if (index >= 0) {
          const updated_alerts = [...current];
          updated_alerts[index] = updated;
          this.alerts.set(updated_alerts);
        }
      },
    });
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

  flyToAlert(alert: Alert): void {
    if (alert.position) {
      console.log('Fly to alert position:', alert.position);
      // TODO: communicate with map component to fly to position
    }
  }

  getSeverityIcon(severity: AlertSeverity): string {
    switch (severity) {
      case AlertSeverity.CRITICAL: return '!!';
      case AlertSeverity.HIGH: return '!';
      case AlertSeverity.MEDIUM: return '\u{26A0}';
      case AlertSeverity.LOW: return 'i';
      case AlertSeverity.INFO: return '\u{2139}';
      default: return '?';
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
