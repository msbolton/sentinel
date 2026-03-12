import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { SettingsComponent } from './settings.component';
import { AuthService } from '../../core/services/auth.service';
import { BehaviorSubject } from 'rxjs';

const mockProfile = {
  username: 'admin',
  email: 'admin@sentinel.local',
  roles: ['sentinel-admin', 'sentinel-analyst', 'classification-ts'],
  classificationLevel: 'TOP SECRET',
};

const mockNonAdminProfile = {
  username: 'viewer',
  email: 'viewer@sentinel.local',
  roles: ['sentinel-viewer', 'classification-u'],
  classificationLevel: 'UNCLASSIFIED',
};

describe('SettingsComponent', () => {
  let component: SettingsComponent;
  let fixture: ComponentFixture<SettingsComponent>;
  let httpMock: HttpTestingController;
  let profileSubject: BehaviorSubject<any>;

  function setup(profile = mockProfile) {
    profileSubject = new BehaviorSubject(profile);

    TestBed.configureTestingModule({
      imports: [SettingsComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: AuthService,
          useValue: {
            userProfile$: profileSubject.asObservable(),
          },
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(SettingsComponent);
    component = fixture.componentInstance;
    httpMock = TestBed.inject(HttpTestingController);
  }

  afterEach(() => {
    httpMock.verify();
  });

  describe('Profile tab', () => {
    beforeEach(() => setup());

    it('should default to profile tab', () => {
      fixture.detectChanges();
      expect(component.activeTab()).toBe('profile');
    });

    it('should display user profile info', () => {
      fixture.detectChanges();
      const el = fixture.nativeElement;
      expect(el.textContent).toContain('admin');
      expect(el.textContent).toContain('admin@sentinel.local');
    });
  });

  describe('User Management tab (admin)', () => {
    beforeEach(() => setup());

    it('should show User Management tab for admin users', () => {
      fixture.detectChanges();
      const tabs = fixture.nativeElement.querySelectorAll('.tab-btn');
      expect(tabs.length).toBe(2);
      expect(tabs[1].textContent).toContain('User Management');
    });

    it('should load pending and active users when switching to management tab', () => {
      fixture.detectChanges();
      component.switchTab('management');
      fixture.detectChanges();

      const pendingReq = httpMock.expectOne('/api/v1/auth/pending-registrations');
      expect(pendingReq.request.method).toBe('GET');
      pendingReq.flush([]);

      const usersReq = httpMock.expectOne('/api/v1/auth/users');
      expect(usersReq.request.method).toBe('GET');
      usersReq.flush([]);
    });

    it('should send classification level when approving', () => {
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([
        { id: 'u1', username: 'p1', email: 'p@t.com', firstName: 'P', lastName: '1', organization: 'O', justification: 'J', registrationDate: '2026-03-11' },
      ]);
      httpMock.expectOne('/api/v1/auth/users').flush([]);
      fixture.detectChanges();

      component.setPendingClassification('u1', 'classification-ts');
      component.approve('u1');

      const approveReq = httpMock.expectOne('/api/v1/auth/approve-registration/u1');
      expect(approveReq.request.body).toEqual({ classificationLevel: 'classification-ts' });
      approveReq.flush({});
      // After approve, it reloads active users
      httpMock.expectOne('/api/v1/auth/users').flush([]);
    });

    it('should load active users and allow classification change', () => {
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([]);
      httpMock.expectOne('/api/v1/auth/users').flush([
        { id: 'a1', username: 'jdoe', email: 'j@e.com', firstName: 'J', lastName: 'D', classificationLevel: 'classification-u', roles: [] },
      ]);
      fixture.detectChanges();

      component.updateClassification('a1', 'classification-ts');

      const updateReq = httpMock.expectOne('/api/v1/auth/users/a1/classification');
      expect(updateReq.request.method).toBe('PUT');
      expect(updateReq.request.body).toEqual({ classificationLevel: 'classification-ts' });
      updateReq.flush({});

      expect(component.activeUsers()[0].classificationLevel).toBe('classification-ts');
    });

    it('should reject a user and remove from list', () => {
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([
        { id: 'u1', username: 'p1', email: 'p@t.com', firstName: 'P', lastName: '1', organization: 'O', justification: 'J', registrationDate: '2026-03-11' },
      ]);
      httpMock.expectOne('/api/v1/auth/users').flush([]);
      fixture.detectChanges();

      component.reject('u1');

      const rejectReq = httpMock.expectOne('/api/v1/auth/reject-registration/u1');
      expect(rejectReq.request.method).toBe('POST');
      rejectReq.flush({});

      expect(component.pendingUsers().length).toBe(0);
    });

    it('should show error when loading pending users fails', () => {
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations')
        .flush({ message: 'Server Error' }, { status: 500, statusText: 'Error' });
      httpMock.expectOne('/api/v1/auth/users').flush([]);
      fixture.detectChanges();

      expect(component.errorMessage()).toBeTruthy();
    });

    it('should handle active user with null classificationLevel', () => {
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([]);
      httpMock.expectOne('/api/v1/auth/users').flush([
        { id: 'a1', username: 'newuser', email: 'n@e.com', firstName: 'N', lastName: 'U', classificationLevel: null, roles: [] },
      ]);
      fixture.detectChanges();

      expect(component.activeUsers()[0].classificationLevel).toBeNull();
    });

    it('should not reload data on repeated tab switches', () => {
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectOne('/api/v1/auth/pending-registrations').flush([]);
      httpMock.expectOne('/api/v1/auth/users').flush([]);

      component.switchTab('profile');
      component.switchTab('management');
      fixture.detectChanges();

      httpMock.expectNone('/api/v1/auth/pending-registrations');
      httpMock.expectNone('/api/v1/auth/users');
    });
  });

  describe('Non-admin user', () => {
    beforeEach(() => setup(mockNonAdminProfile));

    it('should NOT show User Management tab for non-admin', () => {
      fixture.detectChanges();
      const tabs = fixture.nativeElement.querySelectorAll('.tab-btn');
      expect(tabs.length).toBe(1);
      expect(tabs[0].textContent).toContain('Profile');
    });
  });
});
