import { ComponentFixture, TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { Subject, of } from 'rxjs';
import { FindingsComponent } from './findings.component';
import { AnalyticsService } from '../../core/services/analytics.service';
import { AuthService } from '../../core/services/auth.service';
import { LocationService } from '../../core/services/location.service';
import { Finding } from '../../shared/models/finding.model';

function makeFinding(over: Partial<Finding> & { id: string }): Finding {
  return {
    entityId: 'e1234567-0000-4000-8000-000000000000',
    kind: 'UNUSUAL_PLACE',
    severity: 'MEDIUM',
    score: 2.1,
    summary: '3.3 km from place 1, which it usually stays within 1.0 km of',
    expected: { rangeP95M: 1000, thresholdM: 2000, baseline: { mature: true, pointCount: 3360, days: 10 } },
    actual: { distanceM: 3300, lat: 40.03, lon: -74 },
    thresholds: {},
    position: { lat: 40.03, lon: -74 },
    evidenceStart: '2026-10-17T12:00:00Z',
    evidenceEnd: '2026-10-17T12:10:00Z',
    status: 'OPEN',
    classification: 'UNCLASSIFIED',
    hits: 3,
    firstAt: '2026-10-17T12:00:00Z',
    lastAt: new Date(Date.now() - 5 * 60_000).toISOString(),
    ...over,
  };
}

describe('FindingsComponent', () => {
  let fixture: ComponentFixture<FindingsComponent>;
  let el: HTMLElement;
  let analytics: { getFindings: jest.Mock; reviewFinding: jest.Mock; openCount: ReturnType<typeof signal<number | null>> };
  let auth: { hasAnyRole: jest.Mock };
  let flyTo: jest.Mock;

  async function setup(roles: string[], findings: Finding[]): Promise<void> {
    analytics = {
      getFindings: jest.fn().mockReturnValue(of({ data: findings, total: findings.length, page: 1, pageSize: 100 })),
      reviewFinding: jest.fn((id: string, status: string) => of(makeFinding({ id, status: status as Finding['status'], reviewedBy: 'analyst-1' }))),
      openCount: signal<number | null>(findings.filter((f) => f.status === 'OPEN').length),
    };
    auth = { hasAnyRole: jest.fn((...wanted: string[]) => wanted.some((r) => roles.includes(r))) };
    flyTo = jest.fn();
    await TestBed.configureTestingModule({
      imports: [FindingsComponent],
      providers: [
        { provide: AnalyticsService, useValue: analytics },
        { provide: AuthService, useValue: auth },
        { provide: LocationService, useValue: { flyTo, flyTo$: new Subject() } },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(FindingsComponent);
    el = fixture.nativeElement;
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
  }

  it('lists open findings with kind, severity and summary', async () => {
    await setup(['sentinel-viewer'], [makeFinding({ id: 'f1' }), makeFinding({ id: 'f2', kind: 'WENT_DARK', severity: 'HIGH' })]);
    const cards = el.querySelectorAll('.finding-card');
    expect(cards.length).toBe(2);
    expect(cards[0].textContent).toContain('Unusual place');
    expect(cards[0].textContent).toContain('MEDIUM');
    expect(cards[0].textContent).toContain('3 hits');
    expect(cards[1].textContent).toContain('Went dark');
    expect(analytics.getFindings).toHaveBeenCalledWith(expect.objectContaining({ status: 'OPEN' }));
  });

  it('a viewer sees no review buttons', async () => {
    await setup(['sentinel-viewer'], [makeFinding({ id: 'f1' })]);
    expect(el.querySelector('.finding-actions .btn-secondary')).toBeNull();
  });

  it('an analyst can confirm, and the confirmed finding leaves the open view', async () => {
    await setup(['sentinel-analyst'], [makeFinding({ id: 'f1' })]);
    const confirm = el.querySelector('.finding-actions .btn-secondary') as HTMLButtonElement;
    expect(confirm?.textContent).toContain('Confirm');
    confirm.click();
    expect(analytics.reviewFinding).toHaveBeenCalledWith('f1', 'CONFIRMED');
    fixture.detectChanges();
    // Confirmed findings leave the OPEN view.
    expect(el.querySelectorAll('.finding-card').length).toBe(0);
  });

  it('a filter chip reloads with that filter', async () => {
    await setup(['sentinel-analyst'], [makeFinding({ id: 'f1' })]);
    const chips = Array.from(el.querySelectorAll('.filter-chip')) as HTMLButtonElement[];
    chips.find((c) => c.textContent?.trim() === 'HIGH')!.click();
    expect(analytics.getFindings).toHaveBeenLastCalledWith(expect.objectContaining({ severity: 'HIGH', status: 'OPEN' }));
    chips.find((c) => c.textContent?.trim() === 'Went dark')!.click();
    expect(analytics.getFindings).toHaveBeenLastCalledWith(expect.objectContaining({ kinds: ['WENT_DARK'] }));
  });

  it('expanding a card shows expected and seen values, and show on map flies there', async () => {
    await setup(['sentinel-viewer'], [makeFinding({ id: 'f1' })]);
    (el.querySelector('.finding-card') as HTMLElement).click();
    fixture.detectChanges();
    const evidence = el.querySelector('.evidence')!;
    expect(evidence.textContent).toContain('Expected');
    expect(evidence.textContent).toContain('2.0 km');
    expect(evidence.textContent).toContain('3.3 km');
    expect(evidence.textContent).toContain('3360 fixes over 10 days');
    const show = Array.from(el.querySelectorAll('.finding-actions .btn')).find((b) => b.textContent?.includes('Show on map')) as HTMLButtonElement;
    show.click();
    expect(flyTo).toHaveBeenCalledWith(expect.objectContaining({ latitude: 40.03, longitude: -74 }));
  });

  it('shows an empty state when there is nothing', async () => {
    await setup(['sentinel-viewer'], []);
    expect(el.querySelector('.empty-state')?.textContent).toContain('No findings');
  });
});
