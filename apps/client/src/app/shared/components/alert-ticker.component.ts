import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { catchError, of } from 'rxjs';
import { AlertService } from '../../core/services/alert.service';
import { SelectionService } from '../../core/services/selection.service';
import { pushAlert } from '../alert-ticker';
import { Alert, AlertSeverity } from '../models/alert.model';

const TICKER_MAX = 5;

/** UTC hours and minutes as HHMMZ. */
function formatHhmmz(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '----Z';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${pad(d.getUTCHours())}${pad(d.getUTCMinutes())}Z`;
}

/**
 * A row of short alert boxes along the top centre of the map, newest first.
 * Clicking one selects the alerted track. Renders nothing when there are no
 * open alerts; only the boxes take pointer events.
 */
@Component({
  selector: 'app-alert-ticker',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @for (alert of alerts(); track alert.id) {
      <button
        type="button"
        class="tick"
        [class.h]="isHigh(alert)"
        [class.w]="isMedium(alert)"
        [attr.aria-label]="label(alert) + ', ' + alert.severity"
        (click)="select(alert)">{{ label(alert) }}</button>
    }
  `,
  styles: [`
    :host {
      position: absolute;
      top: 22px;
      left: 50%;
      transform: translateX(-50%);
      z-index: 60;
      display: flex;
      gap: 1px;
      pointer-events: none;
    }

    .tick {
      pointer-events: auto;
      font: 12px var(--font-mono);
      color: var(--text-primary);
      background: var(--bg-panel);
      border: 1px solid var(--border-color);
      border-left: 2px solid var(--accent-cyan);
      border-radius: 0;
      padding: 5px 12px;
      white-space: nowrap;
      cursor: pointer;
    }

    .tick.w { border-left-color: var(--accent-yellow); }
    .tick.h { border-left-color: var(--accent-red); }

    .tick:focus-visible {
      outline: 1px solid var(--accent-cyan);
      outline-offset: 0;
    }
  `],
})
export class AlertTickerComponent {
  private readonly selection = inject(SelectionService);
  private readonly service = inject(AlertService);

  readonly alerts = signal<Alert[]>([]);

  constructor() {
    this.service
      .getAlerts({ acknowledged: false, limit: TICKER_MAX })
      .pipe(
        catchError(() => of({ data: [] as Alert[] })),
        takeUntilDestroyed(),
      )
      .subscribe((page) => {
        // An alert that streamed in before the fetch returned stays; the rest fill in behind it.
        const live = this.alerts();
        const seed = [...page.data]
          .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))
          .filter((a) => !live.some((l) => l.id === a.id));
        this.alerts.set([...live, ...seed].slice(0, TICKER_MAX));
      });

    this.service.alertStream$
      .pipe(takeUntilDestroyed())
      .subscribe((alert) => this.alerts.update((list) => pushAlert(list, alert, TICKER_MAX)));
  }

  isHigh(a: Alert): boolean {
    return a.severity === AlertSeverity.CRITICAL || a.severity === AlertSeverity.HIGH;
  }

  isMedium(a: Alert): boolean {
    return a.severity === AlertSeverity.MEDIUM;
  }

  label(a: Alert): string {
    const entity = a.entityName ? ` · ${a.entityName.toUpperCase()}` : '';
    return `${formatHhmmz(a.createdAt)} ${a.title.toUpperCase()}${entity}`;
  }

  select(a: Alert): void {
    if (a.entityId) this.selection.selectById(a.entityId);
  }
}
