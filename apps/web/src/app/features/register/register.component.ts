import { Component, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { HttpClient } from '@angular/common/http';

interface RegisterForm {
  username: string;
  email: string;
  password: string;
  confirmPassword: string;
  firstName: string;
  lastName: string;
  organization: string;
  justification: string;
}

@Component({
  selector: 'app-register',
  standalone: true,
  imports: [FormsModule, RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="register-page">
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

      <!-- Register card -->
      <div class="register-card">
        @if (submitted()) {
          <!-- Success state -->
          <div class="success-state">
            <div class="success-icon">
              <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
                <circle cx="12" cy="12" r="10" stroke="rgba(59,130,246,0.5)" stroke-width="1.5"/>
                <path d="M8 12.5L10.5 15L16 9" stroke="#3b82f6" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </div>
            <h1 class="app-title">SENTINEL</h1>
            <p class="app-subtitle">Geospatial Intelligence Platform</p>
            <div class="divider"></div>
            <p class="success-message">Registration request submitted.</p>
            <p class="success-detail">Your account request is pending administrator approval. You will be notified when access is granted.</p>
            <a class="sign-in-btn return-link" routerLink="/login">Return to Sign In</a>
          </div>
        } @else {
          <!-- Registration form -->
          <div class="logo-mark">
            <div class="crosshair">
              <div class="crosshair-dot"></div>
            </div>
          </div>
          <h1 class="app-title">SENTINEL</h1>
          <p class="app-subtitle">Geospatial Intelligence Platform</p>
          <div class="divider"></div>
          <form class="register-form" (ngSubmit)="onSubmit()">
            <div class="form-row">
              <div class="input-group">
                <label class="input-label" for="firstName">FIRST NAME</label>
                <input
                  id="firstName"
                  class="register-input"
                  type="text"
                  [(ngModel)]="form.firstName"
                  name="firstName"
                  placeholder="First name"
                  [disabled]="loading()" />
              </div>
              <div class="input-group">
                <label class="input-label" for="lastName">LAST NAME</label>
                <input
                  id="lastName"
                  class="register-input"
                  type="text"
                  [(ngModel)]="form.lastName"
                  name="lastName"
                  placeholder="Last name"
                  [disabled]="loading()" />
              </div>
            </div>
            <div class="input-group">
              <label class="input-label" for="username">USERNAME</label>
              <input
                id="username"
                class="register-input"
                type="text"
                [(ngModel)]="form.username"
                name="username"
                autocomplete="username"
                placeholder="Choose a username"
                [disabled]="loading()" />
            </div>
            <div class="input-group">
              <label class="input-label" for="email">EMAIL</label>
              <input
                id="email"
                class="register-input"
                type="email"
                [(ngModel)]="form.email"
                name="email"
                autocomplete="email"
                placeholder="Work email address"
                [disabled]="loading()" />
            </div>
            <div class="input-group">
              <label class="input-label" for="organization">ORGANIZATION</label>
              <input
                id="organization"
                class="register-input"
                type="text"
                [(ngModel)]="form.organization"
                name="organization"
                placeholder="Agency or organization"
                [disabled]="loading()" />
            </div>
            <div class="form-row">
              <div class="input-group">
                <label class="input-label" for="password">PASSWORD</label>
                <input
                  id="password"
                  class="register-input"
                  type="password"
                  [(ngModel)]="form.password"
                  name="password"
                  autocomplete="new-password"
                  placeholder="Min 8 characters"
                  [disabled]="loading()" />
              </div>
              <div class="input-group">
                <label class="input-label" for="confirmPassword">CONFIRM PASSWORD</label>
                <input
                  id="confirmPassword"
                  class="register-input"
                  type="password"
                  [(ngModel)]="form.confirmPassword"
                  name="confirmPassword"
                  autocomplete="new-password"
                  placeholder="Repeat password"
                  [disabled]="loading()" />
              </div>
            </div>
            <div class="input-group">
              <label class="input-label" for="justification">ACCESS JUSTIFICATION</label>
              <textarea
                id="justification"
                class="register-input register-textarea"
                [(ngModel)]="form.justification"
                name="justification"
                placeholder="Describe your need for access to this system"
                rows="3"
                [disabled]="loading()"></textarea>
            </div>
            @if (errorMessage()) {
              <p class="error-message">{{ errorMessage() }}</p>
            }
            <button
              class="sign-in-btn"
              type="submit"
              [disabled]="loading() || !isFormValid()">
              @if (loading()) {
                SUBMITTING...
              } @else {
                REQUEST ACCESS
              }
            </button>
          </form>
          <p class="auth-notice">
            Already have an account?
            <a class="sign-in-link" routerLink="/login">Sign in</a>
          </p>
        }
      </div>
    </div>
  `,
  styles: [`
    :host {
      display: block;
      width: 100vw;
      height: 100vh;
      overflow: auto;
    }

    .register-page {
      position: relative;
      min-height: 100%;
      background: linear-gradient(135deg, #060e1f 0%, #0e1e3d 50%, #091428 100%);
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 40px 16px;
    }

    /* Grid background */
    .bg-grid {
      position: fixed;
      inset: 0;
      pointer-events: none;
      background-image:
        linear-gradient(rgba(59, 130, 246, 0.07) 1px, transparent 1px),
        linear-gradient(90deg, rgba(59, 130, 246, 0.07) 1px, transparent 1px);
      background-size: 60px 60px;
    }

    /* Radar circles */
    .radar-container {
      position: fixed;
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
      position: fixed;
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
      position: fixed;
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

    /* Register card */
    .register-card {
      position: relative;
      z-index: 10;
      width: 100%;
      max-width: 480px;
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

    .register-form {
      display: flex;
      flex-direction: column;
      gap: 14px;
    }

    .form-row {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 12px;
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

    .register-input {
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

    .register-textarea {
      resize: vertical;
      min-height: 80px;
    }

    .register-input::placeholder {
      color: rgba(255, 255, 255, 0.2);
    }

    .register-input:focus {
      border-color: rgba(59, 130, 246, 0.5);
      background: rgba(255, 255, 255, 0.08);
    }

    .register-input:disabled {
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
      display: block;
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
      text-decoration: none;
      text-align: center;
      margin-top: 4px;
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

    .return-link {
      display: inline-block;
      width: auto;
      margin-top: 24px;
    }

    .auth-notice {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 12px;
      color: rgba(255, 255, 255, 0.3);
      margin: 16px 0 0;
    }

    .sign-in-link {
      color: rgba(59, 130, 246, 0.8);
      text-decoration: none;
      transition: color 0.2s ease;
    }

    .sign-in-link:hover {
      color: #3b82f6;
    }

    /* Success state */
    .success-state {
      display: flex;
      flex-direction: column;
      align-items: center;
    }

    .success-icon {
      width: 64px;
      height: 64px;
      margin: 0 auto 18px;
    }

    .success-icon svg {
      width: 100%;
      height: 100%;
    }

    .success-message {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 16px;
      font-weight: 600;
      color: rgba(255, 255, 255, 0.9);
      margin: 0 0 12px;
    }

    .success-detail {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 13px;
      color: rgba(255, 255, 255, 0.4);
      margin: 0;
      line-height: 1.6;
    }
  `],
})
export class RegisterComponent {
  private readonly http = inject(HttpClient);

  form: RegisterForm = {
    username: '',
    email: '',
    password: '',
    confirmPassword: '',
    firstName: '',
    lastName: '',
    organization: '',
    justification: '',
  };

  loading = signal(false);
  errorMessage = signal('');
  submitted = signal(false);

  isFormValid(): boolean {
    return (
      !!this.form.username &&
      !!this.form.email &&
      !!this.form.password &&
      !!this.form.confirmPassword &&
      !!this.form.firstName &&
      !!this.form.lastName &&
      !!this.form.organization &&
      !!this.form.justification
    );
  }

  onSubmit(): void {
    if (!this.isFormValid()) return;

    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(this.form.email)) {
      this.errorMessage.set('Please enter a valid email address');
      return;
    }

    if (this.form.password !== this.form.confirmPassword) {
      this.errorMessage.set('Passwords do not match');
      return;
    }

    if (this.form.password.length < 8) {
      this.errorMessage.set('Password must be at least 8 characters');
      return;
    }

    this.loading.set(true);
    this.errorMessage.set('');

    this.http.post('/api/v1/auth/register', {
      username: this.form.username,
      email: this.form.email,
      password: this.form.password,
      confirmPassword: this.form.confirmPassword,
      firstName: this.form.firstName,
      lastName: this.form.lastName,
      organization: this.form.organization,
      justification: this.form.justification,
    }).subscribe({
      next: () => {
        this.loading.set(false);
        this.submitted.set(true);
      },
      error: (err) => {
        this.loading.set(false);
        if (err.status === 409) {
          this.errorMessage.set('Username or email already exists');
        } else {
          this.errorMessage.set(err.error?.message ?? 'Registration failed. Please try again.');
        }
      },
    });
  }
}
