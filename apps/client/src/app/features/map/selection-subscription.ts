/** The part of WebSocketService a selection subscription uses. */
export interface EntitySubscriber {
  subscribeToEntity(entityId: string): void;
  unsubscribeFromEntity(entityId: string): void;
}

/**
 * Keeps the gateway sending updates for the selected entity wherever it is.
 * A view wider than the detail cap gets live positions only for entities it
 * subscribed to, so without this a selected aircraft stops moving when the
 * map zooms out. One subscription at a time: selecting another entity moves
 * it, deselecting drops it. The WebSocket service replays it on reconnect.
 */
export class SelectionSubscription {
  private current: string | null = null;

  constructor(private readonly ws: EntitySubscriber) {}

  follow(entityId: string | null): void {
    if (entityId === this.current) return;
    if (this.current) this.ws.unsubscribeFromEntity(this.current);
    if (entityId) this.ws.subscribeToEntity(entityId);
    this.current = entityId;
  }
}
