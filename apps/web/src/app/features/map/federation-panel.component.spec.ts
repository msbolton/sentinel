import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FederationPanelComponent } from './federation-panel.component';
import { FederationService } from '../../core/services/federation.service';
import { signal } from '@angular/core';

describe('FederationPanelComponent', () => {
  let component: FederationPanelComponent;
  let fixture: ComponentFixture<FederationPanelComponent>;

  const mockFederationService = {
    peers: signal([
      {
        instanceId: 'peer-1',
        displayName: 'Bravo Station',
        status: 'connected' as const,
        color: '#f97316',
        entityCount: 12,
        userCount: 2,
      },
      {
        instanceId: 'peer-2',
        displayName: 'Charlie HQ',
        status: 'stale' as const,
        color: '#8b5cf6',
        entityCount: 8,
        userCount: 1,
      },
      {
        instanceId: 'peer-3',
        displayName: 'Delta Outpost',
        status: 'disconnected' as const,
        color: '#06b6d4',
        entityCount: 0,
        userCount: 0,
      },
    ]),
    federationActive: signal(true),
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [FederationPanelComponent],
      providers: [
        { provide: FederationService, useValue: mockFederationService },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(FederationPanelComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should show collapsed badge when panel is closed', () => {
    const badge = fixture.nativeElement.querySelector('.federation-badge');
    expect(badge).toBeTruthy();
    const panel = fixture.nativeElement.querySelector('.federation-panel');
    expect(panel).toBeNull();
  });

  it('should show status dots for each peer in badge', () => {
    const dots = fixture.nativeElement.querySelectorAll('.federation-badge .badge-dot');
    expect(dots.length).toBe(3);
  });

  it('should expand panel on badge click', () => {
    const badge = fixture.nativeElement.querySelector('.federation-badge');
    badge.click();
    fixture.detectChanges();
    const panel = fixture.nativeElement.querySelector('.federation-panel');
    expect(panel).toBeTruthy();
  });

  it('should display peer names in expanded panel', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('Bravo Station');
    expect(text).toContain('Charlie HQ');
  });

  it('should show entity counts in expanded panel', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('12 entities');
  });

  it('should show status text for non-connected peers', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('Stale');
    expect(text).toContain('Disconnected');
  });

  it('should show visibility checkboxes in expanded panel', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const checkboxes = fixture.nativeElement.querySelectorAll('.visibility-section input[type="checkbox"]');
    expect(checkboxes.length).toBe(3);
  });

  it('should emit togglePeer event when checkbox changes', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const spy = jest.spyOn(component.togglePeer, 'emit');
    const checkbox = fixture.nativeElement.querySelector('.visibility-section input[type="checkbox"]');
    checkbox.checked = false;
    checkbox.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    expect(spy).toHaveBeenCalledWith({ instanceId: 'peer-1', visible: false });
  });

  it('should show source legend with Local and only connected peers', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const legendItems = fixture.nativeElement.querySelectorAll('.legend-item');
    // Local + 1 connected peer (Bravo Station) = 2 items
    expect(legendItems.length).toBe(2);
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('Local');
    expect(text).toContain('Bravo Station');
  });

  it('should close panel when close button is clicked', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const closeBtn = fixture.nativeElement.querySelector('.panel-close');
    closeBtn.click();
    fixture.detectChanges();
    const panel = fixture.nativeElement.querySelector('.federation-panel');
    expect(panel).toBeNull();
  });

  it('should not render when federation is inactive', () => {
    mockFederationService.federationActive.set(false);
    fixture.detectChanges();
    const badge = fixture.nativeElement.querySelector('.federation-badge');
    const panel = fixture.nativeElement.querySelector('.federation-panel');
    expect(badge).toBeNull();
    expect(panel).toBeNull();
    // Reset
    mockFederationService.federationActive.set(true);
  });
});
