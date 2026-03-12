import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Router } from '@angular/router';
import { provideRouter } from '@angular/router';
import { RegisterComponent } from './register.component';

describe('RegisterComponent', () => {
  let component: RegisterComponent;
  let fixture: ComponentFixture<RegisterComponent>;
  let httpMock: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [RegisterComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([]),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(RegisterComponent);
    component = fixture.componentInstance;
    httpMock = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should render all form fields (7 inputs + 1 textarea = 8)', () => {
    const inputs = fixture.nativeElement.querySelectorAll('input');
    const textareas = fixture.nativeElement.querySelectorAll('textarea');
    expect(inputs.length).toBe(7);
    expect(textareas.length).toBe(1);
  });

  it('should render SENTINEL title', () => {
    const title = fixture.nativeElement.querySelector('.app-title');
    expect(title.textContent).toContain('SENTINEL');
  });

  it('should disable submit when required fields are empty', () => {
    const button = fixture.nativeElement.querySelector('button[type="submit"]');
    expect(button.disabled).toBe(true);
  });

  it('should show invalid email error', () => {
    component.form.username = 'testuser';
    component.form.email = 'not-an-email';
    component.form.password = 'password123';
    component.form.confirmPassword = 'password123';
    component.form.firstName = 'Test';
    component.form.lastName = 'User';
    component.form.organization = 'Test Org';
    component.form.justification = 'Need access for work';

    component.onSubmit();

    expect(component.errorMessage()).toBe('Please enter a valid email address');
  });

  it('should show password mismatch error', () => {
    component.form.username = 'testuser';
    component.form.email = 'test@example.com';
    component.form.password = 'password1';
    component.form.confirmPassword = 'password2';
    component.form.firstName = 'Test';
    component.form.lastName = 'User';
    component.form.organization = 'Test Org';
    component.form.justification = 'Need access for work';

    component.onSubmit();

    expect(component.errorMessage()).toBe('Passwords do not match');
  });

  it('should show password too short error', () => {
    component.form.username = 'testuser';
    component.form.email = 'test@example.com';
    component.form.password = 'short';
    component.form.confirmPassword = 'short';
    component.form.firstName = 'Test';
    component.form.lastName = 'User';
    component.form.organization = 'Test Org';
    component.form.justification = 'Need access for work';

    component.onSubmit();

    expect(component.errorMessage()).toBe('Password must be at least 8 characters');
  });

  it('should submit registration and show success', () => {
    component.form.username = 'testuser';
    component.form.email = 'test@example.com';
    component.form.password = 'password123';
    component.form.confirmPassword = 'password123';
    component.form.firstName = 'Test';
    component.form.lastName = 'User';
    component.form.organization = 'Test Org';
    component.form.justification = 'Need access for work';

    component.onSubmit();

    const req = httpMock.expectOne('/api/auth/register');
    expect(req.request.method).toBe('POST');
    req.flush({});

    expect(component.submitted()).toBe(true);
  });

  it('should show server error on failed registration (409)', () => {
    component.form.username = 'testuser';
    component.form.email = 'test@example.com';
    component.form.password = 'password123';
    component.form.confirmPassword = 'password123';
    component.form.firstName = 'Test';
    component.form.lastName = 'User';
    component.form.organization = 'Test Org';
    component.form.justification = 'Need access for work';

    component.onSubmit();

    const req = httpMock.expectOne('/api/auth/register');
    req.flush({ message: 'Conflict' }, { status: 409, statusText: 'Conflict' });

    expect(component.errorMessage()).toBe('Username or email already exists');
    expect(component.submitted()).toBe(false);
  });

  it('should have a link back to login', () => {
    const link = fixture.nativeElement.querySelector('a[routerLink="/login"]');
    expect(link).toBeTruthy();
  });
});
