import { ChangeDetectionStrategy, Component } from '@angular/core';

/**
 * What the /map route renders in the panel outlet: nothing. The shell draws
 * the one map behind every route itself; this placeholder only gives /map a
 * component so its guard still runs.
 */
@Component({
  selector: 'app-map-route',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: '',
})
export class MapRouteComponent {}
