import { ChangeDetectionStrategy, Component, computed, inject, input, signal } from '@angular/core';
import { EmplacementService, errorMessage } from '../../core/services/emplacement.service';
import { Feed } from '../../shared/models/feed.model';
import { normalizeClearance } from '../../shared/models/classification';
import { Emplacement, draftOf, heartbeatBadgeOf, livenessOf, newDraft } from '../../shared/models/emplacement.model';
import { EmplacementEditorComponent } from './emplacement-editor.component';

/**
 * A connection's emplacements, in its expanded row: each with its heartbeat
 * (from the connection's status, the owning replica's truth), its interval,
 * and edit, deactivate and delete. Only a grpc connection takes new ones
 * until the TCP/UDP and TAK heartbeats exist.
 */
@Component({
  selector: 'app-connection-emplacements',
  standalone: true,
  imports: [EmplacementEditorComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './connection-emplacements.component.html',
  styleUrl: './connection-emplacements.component.scss',
})
export class ConnectionEmplacementsComponent {
  readonly feed = input.required<Feed>();
  /** The page clock, so a "silent since" label shows the day when it is not today. */
  readonly now = input<number>(Date.now());

  protected readonly service = inject(EmplacementService);
  protected readonly canWrite = this.service.canWrite;
  protected readonly canDelete = this.service.canDelete;
  protected readonly error = signal<string | null>(null);
  protected readonly bindable = computed(() => this.feed().driver === 'grpc');
  protected readonly rows = computed(() => {
    const feed = this.feed();
    const live = livenessOf(feed.status?.detail);
    const now = this.now();
    return this.service
      .emplacements()
      .filter((e) => e.feedId === feed.id)
      .map((e) => {
        const l = live.get(e.id);
        return { e, badge: heartbeatBadgeOf(e, l, now), intervalSec: l?.intervalSec ?? e.heartbeatIntervalSec ?? null };
      });
  });
  /** The draft this section's editor holds: a new one for this connection, or one of its rows. */
  protected readonly editing = computed(() => {
    const d = this.service.draft();
    return d && d.feedId === this.feed().id ? d : null;
  });

  startNew(): void {
    this.error.set(null);
    this.service.select(null);
    const config = this.feed().config ?? {};
    this.service.draft.set({
      ...newDraft(),
      feedId: this.feed().id,
      exercise: config['exercise'] === true,
      classification: normalizeClearance(String(config['classification'] ?? 'UNCLASSIFIED')),
    });
  }

  edit(e: Emplacement): void {
    this.error.set(null);
    this.service.select(e.id);
    this.service.draft.set(draftOf(e));
  }

  deactivate(e: Emplacement): void {
    this.error.set(null);
    this.service.update(e.id, e.version, { active: false }).subscribe({ error: (err) => this.error.set(errorMessage(err)) });
  }

  remove(e: Emplacement): void {
    this.error.set(null);
    this.service.remove(e.id).subscribe({
      next: () => {
        if (this.service.draft()?.id === e.id) this.service.draft.set(null);
      },
      error: (err) => this.error.set(errorMessage(err)),
    });
  }
}
