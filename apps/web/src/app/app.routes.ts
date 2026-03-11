import { Routes } from '@angular/router';
import { authGuard, roleGuard } from './core/guards/auth.guard';

export const routes: Routes = [
  {
    path: '',
    pathMatch: 'full',
    redirectTo: 'map',
  },
  {
    path: 'map',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./features/map/map.component').then((m) => m.MapComponent),
  },
  {
    path: 'search',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./features/search/search.component').then((m) => m.SearchComponent),
  },
  {
    path: 'alerts',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./features/alerts/alerts.component').then((m) => m.AlertsComponent),
  },
  {
    path: 'link-graph',
    canActivate: [roleGuard('sentinel-analyst', 'sentinel-admin')],
    loadComponent: () =>
      import('./features/link-graph/link-graph.component').then(
        (m) => m.LinkGraphComponent,
      ),
  },
  {
    path: 'timeline',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./features/timeline/timeline.component').then(
        (m) => m.TimelineComponent,
      ),
  },
  {
    path: 'locations',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./features/locations/locations.component').then(
        (m) => m.LocationsComponent,
      ),
  },
  {
    path: '**',
    redirectTo: 'map',
  },
];
