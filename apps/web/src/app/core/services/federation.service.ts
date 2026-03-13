import { Injectable, OnDestroy, signal, computed } from '@angular/core';
import { Subscription } from 'rxjs';
import {
  WebSocketService,
  FederationPeerStatus,
  PresenceEntry,
} from './websocket.service';

@Injectable({ providedIn: 'root' })
export class FederationService implements OnDestroy {
  /** Currently connected peers with status. */
  readonly peers = signal<FederationPeerStatus[]>([]);

  /** Active remote user presence entries. */
  readonly presenceEntries = signal<PresenceEntry[]>([]);

  /** True if at least one peer is connected. */
  readonly federationActive = computed(() => this.peers().some(p => p.status === 'connected'));

  /** Total federated entity count across all peers. */
  readonly totalFederatedEntities = computed(() =>
    this.peers().reduce((sum, p) => sum + p.entityCount, 0),
  );

  private readonly subscriptions = new Subscription();
  private presenceCleanupTimer: ReturnType<typeof setInterval> | null = null;

  private static readonly PRESENCE_EXPIRY_MS = 5_000;

  constructor(
    private readonly wsService: WebSocketService,
  ) {
    // Subscribe to federation status updates
    this.subscriptions.add(
      this.wsService.federationStatus$.subscribe(event => {
        this.peers.set(event.peers);
      }),
    );

    // Subscribe to presence updates — merge into existing entries
    this.subscriptions.add(
      this.wsService.presenceUpdates$.subscribe(event => {
        const existing = new Map(
          this.presenceEntries().map(e => [e.userId, e]),
        );
        for (const user of event.users) {
          existing.set(user.userId, user);
        }
        this.presenceEntries.set(Array.from(existing.values()));
      }),
    );

    // Periodically expire stale presence entries
    this.presenceCleanupTimer = setInterval(() => {
      if (this.presenceEntries().length === 0) return;
      const now = Date.now();
      const active = this.presenceEntries().filter(
        e => now - e.timestamp < FederationService.PRESENCE_EXPIRY_MS,
      );
      if (active.length !== this.presenceEntries().length) {
        this.presenceEntries.set(active);
      }
    }, 1_000);
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
    if (this.presenceCleanupTimer) {
      clearInterval(this.presenceCleanupTimer);
    }
  }

  /** Get peer color by instance ID. */
  getPeerColor(instanceId: string): string | null {
    return this.peers().find(p => p.instanceId === instanceId)?.color ?? null;
  }
}
