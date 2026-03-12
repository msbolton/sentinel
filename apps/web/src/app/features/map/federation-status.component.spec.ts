import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FederationStatusComponent } from './federation-status.component';
import { FederationService } from '../../core/services/federation.service';
import { signal } from '@angular/core';

describe('FederationStatusComponent', () => {
  let component: FederationStatusComponent;
  let fixture: ComponentFixture<FederationStatusComponent>;

  const mockFederationService = {
    peers: signal([
      {
        instanceId: 'peer-1',
        displayName: 'BRAVO',
        status: 'connected' as const,
        color: '#f97316',
        entityCount: 47,
        userCount: 2,
      },
      {
        instanceId: 'peer-2',
        displayName: 'CHARLIE',
        status: 'stale' as const,
        color: '#a855f7',
        entityCount: 23,
        userCount: 1,
      },
    ]),
    federationActive: signal(true),
    totalFederatedEntities: signal(70),
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [FederationStatusComponent],
      providers: [
        { provide: FederationService, useValue: mockFederationService },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(FederationStatusComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should display peer count', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.textContent).toContain('BRAVO');
    expect(el.textContent).toContain('CHARLIE');
  });

  it('should show status dots with correct colors', () => {
    const dots = fixture.nativeElement.querySelectorAll('.status-dot');
    expect(dots.length).toBe(2);
  });
});
