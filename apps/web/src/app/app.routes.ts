import { Routes } from '@angular/router';

export const routes: Routes = [
  {
    path: '',
    pathMatch: 'full',
    redirectTo: 'map',
  },
  {
    path: 'map',
    loadComponent: () =>
      import('./features/map/map.component').then((m) => m.MapComponent),
  },
  {
    path: 'search',
    loadComponent: () =>
      import('./features/search/search.component').then((m) => m.SearchComponent),
  },
  {
    path: 'alerts',
    loadComponent: () =>
      import('./features/alerts/alerts.component').then((m) => m.AlertsComponent),
  },
  {
    path: 'link-graph',
    loadComponent: () =>
      import('./features/link-graph/link-graph.component').then(
        (m) => m.LinkGraphComponent,
      ),
  },
  {
    path: 'timeline',
    loadComponent: () =>
      import('./features/timeline/timeline.component').then(
        (m) => m.TimelineComponent,
      ),
  },
  {
    path: '**',
    redirectTo: 'map',
  },
];
