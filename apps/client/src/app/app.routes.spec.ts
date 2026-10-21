import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { Router, provideRouter } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';
import { routes } from './app.routes';
import { AuthService } from './core/services/auth.service';

describe('routes', () => {
  it('sends /feeds to /connections, which keeps the operator role guard', async () => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter(routes),
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: AuthService,
          useValue: {
            isAuthenticated: () => true,
            getUserRoles: () => ['sentinel-operator'],
            getClassificationLevel: () => 'UNCLASSIFIED',
          },
        },
      ],
    });

    expect(routes.find((r) => r.path === 'feeds')).toEqual({ path: 'feeds', redirectTo: 'connections' });
    expect(routes.find((r) => r.path === 'connections')?.canActivate?.length).toBe(1);

    const harness = await RouterTestingHarness.create();
    await harness.navigateByUrl('/feeds');
    expect(TestBed.inject(Router).url).toBe('/connections');
  });

  it('keeps viewers without the operator role off /connections', async () => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter(routes),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { isAuthenticated: () => true, getUserRoles: () => ['sentinel-viewer'] } },
      ],
    });

    const harness = await RouterTestingHarness.create();
    await harness.navigateByUrl('/feeds');
    expect(TestBed.inject(Router).url).toBe('/map');
  });

  it('sends /emplacements to the map: emplacements are managed inside their connections', async () => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter(routes),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { isAuthenticated: () => true, getUserRoles: () => ['sentinel-viewer'], getClassificationLevel: () => 'UNCLASSIFIED' } },
      ],
    });
    expect(routes.find((r) => r.path === 'emplacements')).toEqual({ path: 'emplacements', redirectTo: 'map' });
    const harness = await RouterTestingHarness.create();
    await harness.navigateByUrl('/emplacements');
    expect(TestBed.inject(Router).url).toBe('/map');
  });
});
