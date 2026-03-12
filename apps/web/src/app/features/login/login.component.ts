import { Component, OnInit, OnDestroy, inject } from '@angular/core';
import { Router, ActivatedRoute } from '@angular/router';
import { Subscription, filter, take } from 'rxjs';
import { AuthService } from '../../core/services/auth.service';

@Component({
  selector: 'app-login',
  standalone: true,
  template: `
    <div class="login-page">
      <!-- Animated background -->
      <div class="bg-grid"></div>
      <div class="radar-container">
        <div class="radar-circle radar-circle-1"></div>
        <div class="radar-circle radar-circle-2"></div>
        <div class="radar-circle radar-circle-3"></div>
        <div class="radar-sweep"></div>
      </div>
      <div class="entity-dot dot-1"></div>
      <div class="entity-dot dot-2"></div>
      <div class="entity-dot dot-3"></div>
      <div class="entity-dot dot-4"></div>
      <div class="particle particle-1"></div>
      <div class="particle particle-2"></div>

      <!-- Sign-in card -->
      <div class="login-card">
        <div class="logo-mark">
          <div class="crosshair">
            <div class="crosshair-dot"></div>
          </div>
        </div>
        <h1 class="app-title">SENTINEL</h1>
        <p class="app-subtitle">Geospatial Intelligence Platform</p>
        <div class="divider"></div>
        <button class="sign-in-btn" (click)="onSignIn()">SIGN IN</button>
        <p class="auth-notice">Authorized personnel only</p>
      </div>
    </div>
  `,
  styles: [`
    :host {
      display: block;
      width: 100vw;
      height: 100vh;
      overflow: hidden;
    }

    .login-page {
      position: relative;
      width: 100%;
      height: 100%;
      background: linear-gradient(135deg, #060e1f 0%, #0e1e3d 50%, #091428 100%);
      display: flex;
      align-items: center;
      justify-content: center;
    }

    /* Grid background */
    .bg-grid {
      position: absolute;
      inset: 0;
      pointer-events: none;
      background-image:
        linear-gradient(rgba(59, 130, 246, 0.07) 1px, transparent 1px),
        linear-gradient(90deg, rgba(59, 130, 246, 0.07) 1px, transparent 1px);
      background-size: 60px 60px;
    }

    /* Radar circles */
    .radar-container {
      position: absolute;
      top: 50%;
      left: 50%;
      transform: translate(-50%, -50%);
      pointer-events: none;
    }

    .radar-circle {
      position: absolute;
      border-radius: 50%;
      border: 1px solid rgba(59, 130, 246, 0.1);
      top: 50%;
      left: 50%;
      transform: translate(-50%, -50%);
    }

    .radar-circle-1 {
      width: 500px;
      height: 500px;
    }

    .radar-circle-2 {
      width: 350px;
      height: 350px;
      border-color: rgba(59, 130, 246, 0.15);
    }

    .radar-circle-3 {
      width: 200px;
      height: 200px;
    }

    /* Radar sweep */
    .radar-sweep {
      position: absolute;
      top: 50%;
      left: 50%;
      width: 250px;
      height: 250px;
      transform-origin: 0 0;
      background: conic-gradient(
        from 0deg,
        transparent 0deg,
        rgba(59, 130, 246, 0.08) 30deg,
        transparent 60deg
      );
      animation: sweep 8s linear infinite;
    }

    @keyframes sweep {
      from { transform: rotate(0deg); }
      to { transform: rotate(360deg); }
    }

    /* Entity dots */
    .entity-dot {
      position: absolute;
      border-radius: 50%;
      pointer-events: none;
      animation: pulse 3s ease-in-out infinite;
    }

    .dot-1 {
      top: 22%;
      left: 18%;
      width: 6px;
      height: 6px;
      background: #3b82f6;
      box-shadow: 0 0 12px 4px rgba(59, 130, 246, 0.4);
      animation-delay: 0s;
    }

    .dot-2 {
      top: 65%;
      left: 75%;
      width: 5px;
      height: 5px;
      background: #22d3ee;
      box-shadow: 0 0 12px 4px rgba(34, 211, 238, 0.4);
      animation-delay: 1s;
    }

    .dot-3 {
      top: 38%;
      left: 82%;
      width: 4px;
      height: 4px;
      background: #a78bfa;
      box-shadow: 0 0 10px 3px rgba(167, 139, 250, 0.4);
      animation-delay: 2s;
    }

    .dot-4 {
      top: 72%;
      left: 25%;
      width: 5px;
      height: 5px;
      background: #3b82f6;
      box-shadow: 0 0 12px 4px rgba(59, 130, 246, 0.4);
      animation-delay: 0.5s;
    }

    @keyframes pulse {
      0%, 100% { transform: scale(1); opacity: 0.6; }
      50% { transform: scale(1.8); opacity: 1; }
    }

    /* Floating particles */
    .particle {
      position: absolute;
      width: 2px;
      height: 2px;
      border-radius: 50%;
      pointer-events: none;
      background: rgba(59, 130, 246, 0.4);
      animation: drift 12s ease-in-out infinite;
    }

    .particle-1 {
      top: 80%;
      left: 40%;
      animation-delay: 0s;
    }

    .particle-2 {
      top: 70%;
      left: 60%;
      animation-delay: 6s;
    }

    @keyframes drift {
      0%, 100% { transform: translateY(0) translateX(0); opacity: 0; }
      10% { opacity: 0.6; }
      50% { transform: translateY(-120px) translateX(20px); opacity: 0.4; }
      90% { opacity: 0; }
    }

    /* Sign-in card */
    .login-card {
      position: relative;
      z-index: 10;
      width: 340px;
      padding: 40px 36px;
      background: rgba(8, 16, 38, 0.92);
      border: 1px solid rgba(59, 130, 246, 0.2);
      border-radius: 12px;
      text-align: center;
      box-shadow:
        0 0 60px rgba(59, 130, 246, 0.08),
        0 25px 80px rgba(0, 0, 0, 0.5);
      backdrop-filter: blur(12px);
      animation: cardAppear 0.6s ease-out;
    }

    @keyframes cardAppear {
      from {
        opacity: 0;
        transform: translateY(20px) scale(0.97);
      }
      to {
        opacity: 1;
        transform: translateY(0) scale(1);
      }
    }

    /* Logo mark */
    .logo-mark {
      width: 52px;
      height: 52px;
      margin: 0 auto 18px;
      border-radius: 10px;
      background: linear-gradient(135deg, #3b82f6, #2563eb);
      display: flex;
      align-items: center;
      justify-content: center;
      box-shadow: 0 0 24px rgba(59, 130, 246, 0.3);
    }

    .crosshair {
      width: 22px;
      height: 22px;
      border: 2.5px solid rgba(255, 255, 255, 0.9);
      border-radius: 50%;
      position: relative;
    }

    .crosshair-dot {
      position: absolute;
      top: 50%;
      left: 50%;
      transform: translate(-50%, -50%);
      width: 5px;
      height: 5px;
      background: white;
      border-radius: 50%;
    }

    .app-title {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 28px;
      font-weight: 700;
      letter-spacing: 6px;
      color: rgba(255, 255, 255, 0.95);
      margin: 0;
    }

    .app-subtitle {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 12px;
      color: rgba(255, 255, 255, 0.4);
      margin: 6px 0 0;
      letter-spacing: 1.5px;
    }

    .divider {
      width: 100%;
      height: 1px;
      background: linear-gradient(90deg, transparent, rgba(59, 130, 246, 0.3), transparent);
      margin: 28px 0;
    }

    .sign-in-btn {
      width: 100%;
      padding: 13px 24px;
      background: linear-gradient(135deg, #3b82f6, #2563eb);
      color: white;
      border: none;
      border-radius: 8px;
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 14px;
      font-weight: 600;
      letter-spacing: 2px;
      cursor: pointer;
      box-shadow: 0 4px 20px rgba(59, 130, 246, 0.3);
      transition: box-shadow 0.2s ease, transform 0.15s ease;
    }

    .sign-in-btn:hover {
      box-shadow: 0 6px 28px rgba(59, 130, 246, 0.45);
      transform: translateY(-1px);
    }

    .sign-in-btn:active {
      transform: translateY(0);
      box-shadow: 0 2px 12px rgba(59, 130, 246, 0.3);
    }

    .auth-notice {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 11px;
      color: rgba(255, 255, 255, 0.25);
      margin: 16px 0 0;
      letter-spacing: 0.5px;
    }
  `],
})
export class LoginComponent implements OnInit, OnDestroy {
  private readonly authService = inject(AuthService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private authSub: Subscription | null = null;

  ngOnInit(): void {
    this.authSub = this.authService.isAuthenticated$.pipe(
      filter((isAuth) => isAuth),
      take(1),
    ).subscribe(() => {
      const returnUrl = this.route.snapshot.queryParams['returnUrl'] || '/map';
      this.router.navigateByUrl(returnUrl);
    });
  }

  ngOnDestroy(): void {
    this.authSub?.unsubscribe();
  }

  onSignIn(): void {
    const returnUrl = this.route.snapshot.queryParams['returnUrl'] || '/map';
    const redirectUri =
      window.location.origin + window.location.pathname + '#' + returnUrl;
    this.authService.login(redirectUri);
  }
}
