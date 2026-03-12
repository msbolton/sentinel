import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, ActivatedRoute, provideRouter } from '@angular/router';
import { BehaviorSubject } from 'rxjs';
import { LoginComponent } from './login.component';
import { AuthService } from '../../core/services/auth.service';

describe('LoginComponent', () => {
  let component: LoginComponent;
  let fixture: ComponentFixture<LoginComponent>;
  let authService: {
    isAuthenticated$: BehaviorSubject<boolean>;
    loginWithCredentials: jest.Mock;
    isAuthenticated: jest.Mock;
  };
  let router: Router;
  let queryParams: { returnUrl?: string };

  beforeEach(async () => {
    authService = {
      isAuthenticated$: new BehaviorSubject<boolean>(false),
      loginWithCredentials: jest.fn().mockResolvedValue({ success: true }),
      isAuthenticated: jest.fn().mockReturnValue(false),
    };
    queryParams = {};

    await TestBed.configureTestingModule({
      imports: [LoginComponent],
      providers: [
        { provide: AuthService, useValue: authService },
        provideRouter([]),
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { queryParams } },
        },
      ],
    }).compileComponents();

    router = TestBed.inject(Router);
    jest.spyOn(router, 'navigateByUrl').mockResolvedValue(true);

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

  it('should call loginWithCredentials on sign in', async () => {
    fixture.detectChanges();
    component.username = 'operator';
    component.password = 'secret';

    await component.onSignIn();

    expect(authService.loginWithCredentials).toHaveBeenCalledWith('operator', 'secret');
  });

  it('should navigate to /map on successful login', async () => {
    fixture.detectChanges();
    component.username = 'operator';
    component.password = 'secret';

    await component.onSignIn();

    expect(router.navigateByUrl).toHaveBeenCalledWith('/map');
  });

  it('should navigate to returnUrl on successful login', async () => {
    queryParams.returnUrl = '/alerts';
    fixture.detectChanges();
    component.username = 'operator';
    component.password = 'secret';

    await component.onSignIn();

    expect(router.navigateByUrl).toHaveBeenCalledWith('/alerts');
  });

  it('should show error message on failed login', async () => {
    authService.loginWithCredentials.mockResolvedValue({
      success: false,
      error: 'Invalid credentials',
    });
    fixture.detectChanges();
    component.username = 'bad';
    component.password = 'wrong';

    await component.onSignIn();

    expect(component.errorMessage()).toBe('Invalid credentials');
    expect(router.navigateByUrl).not.toHaveBeenCalled();
  });

  it('should not submit when username or password is empty', async () => {
    fixture.detectChanges();
    component.username = '';
    component.password = '';

    await component.onSignIn();

    expect(authService.loginWithCredentials).not.toHaveBeenCalled();
  });

  it('should render username and password inputs', () => {
    fixture.detectChanges();
    const inputs = fixture.nativeElement.querySelectorAll('.login-input');
    expect(inputs.length).toBe(2);
    expect(inputs[0].type).toBe('text');
    expect(inputs[1].type).toBe('password');
  });

  it('should render the SENTINEL title', () => {
    fixture.detectChanges();
    const title = fixture.nativeElement.querySelector('.app-title');
    expect(title.textContent).toContain('SENTINEL');
  });
});
