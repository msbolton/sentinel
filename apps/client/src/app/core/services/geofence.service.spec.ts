import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { of, throwError } from 'rxjs';
import { GeofenceService } from './geofence.service';
import { EntityService } from './entity.service';
import { AuthService } from './auth.service';
import { GeofenceDraft } from '../../shared/models/geofence.model';

const seeded = {
  id: 'r1', name: 'Persian Gulf Exclusion Zone', ruleType: 'GEOFENCE',
  config: { polygon: [[51, 27], [56, 27], [56, 24], [51, 24]], triggerOn: 'ENTRY' },
  monitoredEntityTypes: ['VESSEL', 'AIRCRAFT'], severity: 'HIGH', enabled: true,
  createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z',
};
const speed = { ...seeded, id: 'r2', name: 'Fast movers', ruleType: 'SPEED', config: { maxKnots: 500 } };
const ring = {
  ...seeded, id: 'r3', name: 'Escort bubble',
  config: { shape: 'ring', triggerOn: 'ENTRY', color: '#f59e0b', ring: { entityId: 'e9', entityName: 'MV HORIZON STAR', radiusM: 8000 } },
  monitoredEntityTypes: [], severity: 'MEDIUM',
};

describe('GeofenceService', () => {
  let service: GeofenceService;
  let http: HttpTestingController;
  const entities = { getEntity: jest.fn() };
  const auth = { hasAnyRole: jest.fn().mockReturnValue(true) };

  beforeEach(() => {
    localStorage.clear();
    entities.getEntity.mockReset();
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: EntityService, useValue: entities }, { provide: AuthService, useValue: auth }],
    });
    service = TestBed.inject(GeofenceService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it("loads only geofence rules and fills in the seeded zone's missing shape and colour", () => {
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([seeded, speed]);
    expect(service.zones().map((z) => z.id)).toEqual(['r1']);
    expect(service.zones()[0].config.shape).toBe('polygon');
    expect(service.zones()[0].config.color).toBe('#ef4444');
    expect(service.zoneSummary(service.zones()[0])).toMatch(/^enter · vessels, aircraft · HIGH · 16\d{4} km²$/);
  });

  it('creates a zone and appends it', () => {
    const draft: GeofenceDraft = {
      name: 'Anchorage north', ruleType: 'GEOFENCE',
      config: { shape: 'polygon', polygon: [[55, 25], [56, 25], [56, 24]], triggerOn: 'ENTRY', color: '#f97316' },
      monitoredEntityTypes: ['VESSEL'], severity: 'HIGH', enabled: true,
    };
    service.create(draft).subscribe();
    const req = http.expectOne('/api/v1/alerts/rules');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual(draft);
    req.flush({ ...draft, id: 'r9', createdAt: 'now', updatedAt: 'now' });
    expect(service.zones().map((z) => z.id)).toEqual(['r9']);
  });

  it('toggles enabled with a PUT carrying only enabled', () => {
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([seeded]);
    service.setEnabled('r1', false).subscribe();
    const req = http.expectOne('/api/v1/alerts/rules/r1');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ enabled: false });
    req.flush({ ...seeded, enabled: false });
    expect(service.zones()[0].enabled).toBe(false);
  });

  it('deletes a zone and drops it', () => {
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([seeded]);
    service.delete('r1').subscribe();
    const req = http.expectOne('/api/v1/alerts/rules/r1');
    expect(req.request.method).toBe('DELETE');
    req.flush(null, { status: 204, statusText: 'No Content' });
    expect(service.zones()).toEqual([]);
  });

  it('fetches ring anchors once and marks a missing one as track lost', () => {
    entities.getEntity.mockReturnValue(throwError(() => new Error('404')));
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([ring]);
    expect(entities.getEntity).toHaveBeenCalledTimes(1);
    expect(entities.getEntity).toHaveBeenCalledWith('e9');
    expect(service.anchors().get('e9')).toBeNull();
    expect(service.zoneSummary(service.zones()[0])).toBe('everything within 8.0 km · MEDIUM · follows track · track lost');
    entities.getEntity.mockReturnValue(of({ id: 'e9', name: 'MV HORIZON STAR' }));
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([ring]);
    expect(service.anchors().get('e9')).toEqual({ id: 'e9', name: 'MV HORIZON STAR' });
    expect(service.zoneSummary(service.zones()[0])).toBe('everything within 8.0 km · MEDIUM · follows track');
    expect(service.zoneSummary({ ...service.zones()[0], monitoredEntityTypes: ['VESSEL', 'AIRCRAFT'] })).toBe('vessels, aircraft within 8.0 km · MEDIUM · follows track');
  });

  it('remembers the last colour', () => {
    expect(service.lastColor()).toBe('#ef4444');
    service.rememberColor('#06b6d4');
    expect(service.lastColor()).toBe('#06b6d4');
    expect(localStorage.getItem('sentinel-geofence-color')).toBe('#06b6d4');
  });

  it('lets operators, analysts and admins write zones', () => {
    auth.hasAnyRole.mockReturnValue(false);
    expect(service.canWrite()).toBe(false);
    expect(auth.hasAnyRole).toHaveBeenCalledWith('sentinel-operator', 'sentinel-analyst', 'sentinel-admin');
  });

  it('treats an anchor that is not live as lost and refreshes anchors on every load', () => {
    entities.getEntity.mockReturnValue(of({ id: 'e9', name: 'MV HORIZON STAR', ageoutState: 'AGED_OUT' }));
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([ring]);
    expect(service.anchors().get('e9')).toBeNull();
    entities.getEntity.mockReturnValue(of({ id: 'e9', name: 'MV HORIZON STAR', ageoutState: 'LIVE' }));
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([ring]);
    expect(service.anchors().get('e9')?.id).toBe('e9');
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([ring]);
    expect(entities.getEntity).toHaveBeenCalledTimes(3);
  });

  it('falls back to the default colour for an unparsable one', () => {
    service.load().subscribe();
    http.expectOne('/api/v1/alerts/rules').flush([{ ...seeded, config: { ...seeded.config, color: 'xyz' } }]);
    expect(service.zones()[0].config.color).toBe('#ef4444');
  });
});
