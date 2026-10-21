import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { EmplacementService, errorMessage } from '../../core/services/emplacement.service';
import { FeedService } from '../../core/services/feed.service';
import { Emplacement } from '../../shared/models/emplacement.model';

/**
 * The emplacements with no connection, at the top of the Connections page
 * until there are none. Nothing reports them, so they are inactive: assign
 * each to a gRPC connection (which activates it) or delete it.
 */
@Component({
  selector: 'app-unassigned-emplacements',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './unassigned-emplacements.component.html',
  styleUrl: './unassigned-emplacements.component.scss',
})
export class UnassignedEmplacementsComponent {
  private readonly service = inject(EmplacementService);
  private readonly feeds = inject(FeedService);

  protected readonly canWrite = this.service.canWrite;
  protected readonly canDelete = this.service.canDelete;
  protected readonly rows = computed(() => this.service.emplacements().filter((e) => e.feedId === null));
  /** Only a grpc connection heartbeats its emplacements, so only it takes one. */
  protected readonly targets = computed(() => this.feeds.feeds().filter((f) => f.driver === 'grpc'));
  protected readonly chosen = signal<Record<string, string>>({});
  protected readonly error = signal<string | null>(null);

  protected choose(id: string, event: Event): void {
    const feedId = (event.target as HTMLSelectElement).value;
    this.chosen.update((m) => ({ ...m, [id]: feedId }));
  }

  protected assign(e: Emplacement): void {
    const feedId = this.chosen()[e.id];
    if (!feedId) return;
    this.error.set(null);
    this.service.update(e.id, e.version, { feedId, active: true }).subscribe({ error: (err) => this.error.set(errorMessage(err)) });
  }

  protected remove(e: Emplacement): void {
    this.error.set(null);
    this.service.remove(e.id).subscribe({ error: (err) => this.error.set(errorMessage(err)) });
  }
}
