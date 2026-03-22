import { Component, OnDestroy, inject, signal } from '@angular/core';
import { Router, ActivatedRoute } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';

import { AuthService } from '../../core/services/auth.service';

@Component({
  selector: 'app-login',
  standalone: true,
  imports: [FormsModule, RouterLink],
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
        <form class="login-form" (ngSubmit)="onSignIn()">
          <div class="input-group">
            <label class="input-label" for="username">USERNAME</label>
            <input
              id="username"
              class="login-input"
              type="text"
              [(ngModel)]="username"
              name="username"
              autocomplete="username"
              placeholder="Enter username"
              [disabled]="loading()" />
          </div>
          <div class="input-group">
            <label class="input-label" for="password">PASSWORD</label>
            <input
              id="password"
              class="login-input"
              type="password"
              [(ngModel)]="password"
              name="password"
              autocomplete="current-password"
              placeholder="Enter password"
              [disabled]="loading()" />
          </div>
          @if (errorMessage()) {
            <p class="error-message">{{ errorMessage() }}</p>
          }
          <button
            class="sign-in-btn"
            type="submit"
            [disabled]="loading() || !username || !password">
            @if (loading()) {
              AUTHENTICATING...
            } @else {
              SIGN IN
            }
          </button>
        </form>
        <p class="auth-notice">
          Need access? <a class="request-link" routerLink="/register">Request an account</a>
        </p>
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

    .login-form {
      display: flex;
      flex-direction: column;
      gap: 16px;
    }

    .input-group {
      text-align: left;
    }

    .input-label {
      display: block;
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 10px;
      font-weight: 600;
      letter-spacing: 1.5px;
      color: rgba(255, 255, 255, 0.4);
      margin-bottom: 6px;
    }

    .login-input {
      width: 100%;
      padding: 11px 14px;
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid rgba(59, 130, 246, 0.2);
      border-radius: 8px;
      color: rgba(255, 255, 255, 0.9);
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 14px;
      outline: none;
      transition: border-color 0.2s ease, background 0.2s ease;
      box-sizing: border-box;
    }

    .login-input::placeholder {
      color: rgba(255, 255, 255, 0.2);
    }

    .login-input:focus {
      border-color: rgba(59, 130, 246, 0.5);
      background: rgba(255, 255, 255, 0.08);
    }

    .login-input:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }

    .error-message {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 12px;
      color: #f87171;
      margin: 0;
      text-align: center;
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

    .sign-in-btn:hover:not(:disabled) {
      box-shadow: 0 6px 28px rgba(59, 130, 246, 0.45);
      transform: translateY(-1px);
    }

    .sign-in-btn:active:not(:disabled) {
      transform: translateY(0);
      box-shadow: 0 2px 12px rgba(59, 130, 246, 0.3);
    }

    .sign-in-btn:disabled {
      opacity: 0.6;
      cursor: not-allowed;
    }

    .auth-notice {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 11px;
      color: rgba(255, 255, 255, 0.25);
      margin: 16px 0 0;
      letter-spacing: 0.5px;
    }

    .request-link {
      color: rgba(59, 130, 246, 0.8);
      text-decoration: none;
    }

    .request-link:hover {
      color: rgba(59, 130, 246, 1);
      text-decoration: underline;
    }
  `],
})
export class LoginComponent implements OnDestroy {
  private readonly authService = inject(AuthService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);

  username = '';
  password = '';
  loading = signal(false);
  errorMessage = signal('');

  ngOnDestroy(): void {
  }

  async onSignIn(): Promise<void> {
    if (!this.username || !this.password) return;

    this.loading.set(true);
    this.errorMessage.set('');

    const result = await this.authService.loginWithCredentials(this.username, this.password);

    if (!result.success) {
      this.loading.set(false);
      this.errorMessage.set(result.error ?? 'Authentication failed');
      return;
    }

    const returnUrl = this.route.snapshot.queryParams['returnUrl'] || '/map';
    this.router.navigateByUrl(returnUrl);
  }
}
