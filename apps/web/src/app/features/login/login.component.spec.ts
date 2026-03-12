import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, ActivatedRoute } from '@angular/router';
import { BehaviorSubject } from 'rxjs';
import { LoginComponent } from './login.component';
import { AuthService } from '../../core/services/auth.service';

describe('LoginComponent', () => {
  let component: LoginComponent;
  let fixture: ComponentFixture<LoginComponent>;
  let authService: { isAuthenticated$: BehaviorSubject<boolean>; login: jest.Mock; isAuthenticated: jest.Mock };
  let router: { navigateByUrl: jest.Mock; navigate: jest.Mock };
  let queryParams: { returnUrl?: string };

  beforeEach(async () => {
    authService = {
      isAuthenticated$: new BehaviorSubject<boolean>(false),
      login: jest.fn().mockResolvedValue(undefined),
      isAuthenticated: jest.fn().mockReturnValue(false),
    };
    router = { navigateByUrl: jest.fn(), navigate: jest.fn() };
    queryParams = {};

    await TestBed.configureTestingModule({
      imports: [LoginComponent],
      providers: [
        { provide: AuthService, useValue: authService },
        { provide: Router, useValue: router },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { queryParams } },
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(LoginComponent);
    component = fixture.componentInstance;
  });

  it('should create', () => {
    fixture.detectChanges();
    expect(component).toBeTruthy();
  });

  it('should redirect to /map when already authenticated', () => {
    authService.isAuthenticated$.next(true);
    fixture.detectChanges();

    expect(router.navigateByUrl).toHaveBeenCalledWith('/map');
  });

  it('should redirect to returnUrl when already authenticated', () => {
    queryParams.returnUrl = '/alerts';
    authService.isAuthenticated$.next(true);
    fixture.detectChanges();

    expect(router.navigateByUrl).toHaveBeenCalledWith('/alerts');
  });

  it('should not redirect when not authenticated', () => {
    fixture.detectChanges();

    expect(router.navigateByUrl).not.toHaveBeenCalled();
  });

  it('should call authService.login with redirectUri on sign in', async () => {
    fixture.detectChanges();

    await component.onSignIn();

    const expectedUri = window.location.origin + window.location.pathname + '#/map';
    expect(authService.login).toHaveBeenCalledWith(expectedUri);
  });

  it('should use returnUrl in redirectUri on sign in', async () => {
    queryParams.returnUrl = '/alerts';
    fixture.detectChanges();

    await component.onSignIn();

    const expectedUri = window.location.origin + window.location.pathname + '#/alerts';
    expect(authService.login).toHaveBeenCalledWith(expectedUri);
  });

  it('should navigate directly when login() does not redirect and user is authenticated', async () => {
    authService.isAuthenticated.mockReturnValue(true);
    fixture.detectChanges();

    await component.onSignIn();

    expect(router.navigateByUrl).toHaveBeenCalledWith('/map');
  });

  it('should render the sign-in button', () => {
    fixture.detectChanges();
    const button = fixture.nativeElement.querySelector('.sign-in-btn');
    expect(button).toBeTruthy();
    expect(button.textContent).toContain('SIGN IN');
  });

  it('should render the SENTINEL title', () => {
    fixture.detectChanges();
    const title = fixture.nativeElement.querySelector('.app-title');
    expect(title.textContent).toContain('SENTINEL');
  });
});
