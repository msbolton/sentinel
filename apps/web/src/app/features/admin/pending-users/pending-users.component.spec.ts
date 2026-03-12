import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { PendingUsersComponent, PendingUser } from './pending-users.component';

const mockPendingUsers: PendingUser[] = [
  {
    id: 'u1',
    username: 'pending1',
    email: 'p1@test.com',
    firstName: 'Pending',
    lastName: 'One',
    organization: 'TestOrg',
    justification: 'Need access',
    registrationDate: '2026-03-11T10:00:00Z',
  },
  {
    id: 'u2',
    username: 'pending2',
    email: 'p2@test.com',
    firstName: 'Pending',
    lastName: 'Two',
    organization: 'OtherOrg',
    justification: 'Analyst work',
    registrationDate: '2026-03-10T08:00:00Z',
  },
];

describe('PendingUsersComponent', () => {
  let component: PendingUsersComponent;
  let fixture: ComponentFixture<PendingUsersComponent>;
  let httpMock: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [PendingUsersComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(PendingUsersComponent);
    component = fixture.componentInstance;
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('should create and fetch pending users on init', () => {
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/auth/pending-registrations');
    expect(req.request.method).toBe('GET');
    req.flush(mockPendingUsers);

    fixture.detectChanges();

    expect(component).toBeTruthy();
    expect(component.users().length).toBe(2);
  });

  it('should render a table with pending users', () => {
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/auth/pending-registrations');
    req.flush(mockPendingUsers);

    fixture.detectChanges();

    const rows = fixture.nativeElement.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);
  });

  it('should call approve endpoint and remove user from list', () => {
    fixture.detectChanges();

    const loadReq = httpMock.expectOne('/api/auth/pending-registrations');
    loadReq.flush(mockPendingUsers);
    fixture.detectChanges();

    component.approve('u1');
    fixture.detectChanges();

    const approveReq = httpMock.expectOne('/api/auth/approve-registration/u1');
    expect(approveReq.request.method).toBe('POST');
    approveReq.flush({});

    fixture.detectChanges();

    expect(component.users().find(u => u.id === 'u1')).toBeUndefined();
    expect(component.users().length).toBe(1);
  });

  it('should call reject endpoint and remove user from list', () => {
    fixture.detectChanges();

    const loadReq = httpMock.expectOne('/api/auth/pending-registrations');
    loadReq.flush(mockPendingUsers);
    fixture.detectChanges();

    component.reject('u2');
    fixture.detectChanges();

    const rejectReq = httpMock.expectOne('/api/auth/reject-registration/u2');
    expect(rejectReq.request.method).toBe('POST');
    rejectReq.flush({});

    fixture.detectChanges();

    expect(component.users().find(u => u.id === 'u2')).toBeUndefined();
    expect(component.users().length).toBe(1);
  });

  it('should show error on failed approval', () => {
    fixture.detectChanges();

    const loadReq = httpMock.expectOne('/api/auth/pending-registrations');
    loadReq.flush(mockPendingUsers);
    fixture.detectChanges();

    component.approve('u1');
    fixture.detectChanges();

    const approveReq = httpMock.expectOne('/api/auth/approve-registration/u1');
    approveReq.flush({ message: 'Internal Server Error' }, { status: 500, statusText: 'Internal Server Error' });

    fixture.detectChanges();

    expect(component.errorMessage()).toBeTruthy();
    expect(component.users().find(u => u.id === 'u1')).toBeDefined();
    expect(component.users().length).toBe(2);
  });

  it('should show empty state when no pending users', () => {
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/auth/pending-registrations');
    req.flush([]);

    fixture.detectChanges();

    const emptyState = fixture.nativeElement.querySelector('.empty-state');
    expect(emptyState).toBeTruthy();
  });
});
