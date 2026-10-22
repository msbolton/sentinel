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
    path: 'findings',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./features/findings/findings.component').then((m) => m.FindingsComponent),
  },
  {
    path: 'geofences',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./features/geofences/geofences.component').then((m) => m.GeofencesComponent),
  },
  {
    // Emplacements are managed inside their connections (Connections page).
    path: 'emplacements',
    redirectTo: 'map',
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
    path: 'connections',
    canActivate: [roleGuard('sentinel-operator', 'sentinel-admin')],
    loadComponent: () =>
      import('./features/connections/connections.component').then((m) => m.ConnectionsComponent),
  },
  {
    path: 'feeds',
    redirectTo: 'connections',
  },
  {
    // The public demo's entry: no guard, it signs the visitor in as a guest.
    path: 'demo',
    loadComponent: () =>
      import('./features/demo/demo-start.component').then((m) => m.DemoStartComponent),
  },
  {
    path: 'login',
    loadComponent: () =>
      import('./features/login/login.component').then((m) => m.LoginComponent),
  },
  {
    path: 'register',
    loadComponent: () =>
      import('./features/register/register.component').then((m) => m.RegisterComponent),
  },
  {
    path: 'settings',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./features/settings/settings.component').then(
        (m) => m.SettingsComponent,
      ),
  },
  {
    path: 'admin/users',
    redirectTo: 'settings',
  },
  {
    path: '**',
    redirectTo: 'map',
  },
];
