import { ChangeDetectionStrategy, Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { AnalyticsService } from '../../core/services/analytics.service';
import { AuthService } from '../../core/services/auth.service';
import { LocationService } from '../../core/services/location.service';
import { Location } from '../../shared/models/location.model';
import {
  FINDING_KINDS,
  FINDING_SEVERITIES,
  FINDING_STATUSES,
  Finding,
  FindingKind,
  FindingSeverity,
  FindingStatus,
  evidencePairs,
  findingKindLabel,
} from '../../shared/models/finding.model';

/**
 * Findings: where entities depart from their own baselines. Each row shows
 * what was expected and what was seen; analysts confirm (which raises an
 * alert) or dismiss.
 */
@Component({
  selector: 'app-findings',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule],
  templateUrl: './findings.component.html',
  styleUrls: ['./findings.component.scss'],
})
export class FindingsComponent implements OnInit {
  private readonly analytics = inject(AnalyticsService);
  private readonly auth = inject(AuthService);
  private readonly locations = inject(LocationService);
  private readonly destroyRef = inject(DestroyRef);

  findings = signal<Finding[]>([]);
  total = signal<number>(0);
  loading = signal<boolean>(false);
  error = signal<string | null>(null);
  status = signal<FindingStatus | null>('OPEN');
  kind = signal<FindingKind | null>(null);
  severity = signal<FindingSeverity | null>(null);
  expanded = signal<Set<string>>(new Set());

  readonly statuses = FINDING_STATUSES;
  readonly kinds = FINDING_KINDS;
  readonly severities = FINDING_SEVERITIES;
  readonly canReview = computed(() => this.auth.hasAnyRole('sentinel-analyst', 'sentinel-admin'));
  readonly openCount = this.analytics.openCount;

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading.set(true);
    this.error.set(null);
    this.analytics
      .getFindings({
        status: this.status() ?? undefined,
        kinds: this.kind() ? [this.kind() as FindingKind] : undefined,
        severity: this.severity() ?? undefined,
        pageSize: 100,
      })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.findings.set(res.data ?? []);
          this.total.set(res.total ?? 0);
          this.loading.set(false);
        },
        error: () => {
          this.error.set('Findings are unavailable right now.');
          this.loading.set(false);
        },
      });
  }

  filterByStatus(status: FindingStatus | null): void {
    this.status.set(status);
    this.load();
  }

  filterByKind(kind: FindingKind | null): void {
    this.kind.set(kind);
    this.load();
  }

  filterBySeverity(severity: FindingSeverity | null): void {
    this.severity.set(severity);
    this.load();
  }

  toggle(id: string): void {
    const next = new Set(this.expanded());
    if (next.has(id)) next.delete(id);
    else next.add(id);
    this.expanded.set(next);
  }

  isExpanded(id: string): boolean {
    return this.expanded().has(id);
  }

  review(finding: Finding, status: 'CONFIRMED' | 'DISMISSED', event: Event): void {
    event.stopPropagation();
    this.analytics
      .reviewFinding(finding.id, status)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (updated) => {
          const current = this.findings();
          const keep = this.status() === null || this.status() === updated.status;
          this.findings.set(keep ? current.map((f) => (f.id === updated.id ? updated : f)) : current.filter((f) => f.id !== updated.id));
        },
        error: () => this.error.set('The review could not be saved.'),
      });
  }

  showOnMap(finding: Finding, event: Event): void {
    event.stopPropagation();
    if (!finding.position) return;
    this.locations.flyTo({
      id: finding.id,
      name: finding.summary,
      latitude: finding.position.lat,
      longitude: finding.position.lon,
      altitude: 5000,
      heading: 0,
      pitch: -60,
      range: 8000,
      has3dTiles: false,
      category: 'CUSTOM',
      createdAt: finding.firstAt,
      updatedAt: finding.lastAt,
    } as Location);
  }

  kindLabel(kind: string): string {
    return findingKindLabel(kind);
  }

  expectedPairs(f: Finding): { label: string; value: string }[] {
    return evidencePairs(f.expected);
  }

  actualPairs(f: Finding): { label: string; value: string }[] {
    return evidencePairs(f.actual);
  }

  baselineNote(f: Finding): string | null {
    const b = f.expected?.['baseline'] as { mature?: boolean; pointCount?: number; days?: number } | undefined;
    if (!b) return null;
    if (b.mature === false) return 'Judged without a mature baseline';
    return `Baseline: ${b.pointCount ?? '?'} fixes over ${b.days ?? '?'} days`;
  }

  severityClass(severity: string): string {
    return severity.toLowerCase();
  }

  timeAgo(dateStr: string): string {
    const diffMs = Date.now() - new Date(dateStr).getTime();
    const mins = Math.floor(diffMs / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    return `${Math.floor(hours / 24)}d ago`;
  }
}
