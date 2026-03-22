import { Injectable, OnDestroy } from '@angular/core';
import { BehaviorSubject, Observable, Subject } from 'rxjs';
import { io, Socket } from 'socket.io-client';
import { EntityEvent } from '../../shared/models/entity.model';
import { Alert } from '../../shared/models/alert.model';

export type ConnectionStatus = 'connected' | 'disconnected' | 'reconnecting';

@Injectable({ providedIn: 'root' })
export class WebSocketService implements OnDestroy {
  private socket: Socket | null = null;

  private readonly entityUpdatesSubject = new Subject<EntityEvent>();
  private readonly alertSubject = new Subject<Alert>();
  private readonly connectionStatusSubject = new BehaviorSubject<ConnectionStatus>('disconnected');
  private readonly ageoutSubject = new Subject<{ eventType: string; payload: any }>();

  readonly entityUpdates$ = this.entityUpdatesSubject.asObservable();
  readonly alertStream$ = this.alertSubject.asObservable();
  readonly connectionStatus$ = this.connectionStatusSubject.asObservable();
  readonly ageoutEvents$ = this.ageoutSubject.asObservable();

  connect(): void {
    if (this.socket) {
      return; // Already connected or reconnecting — prevent duplicate listeners
    }

    this.socket = io('/entities', {
      path: '/socket.io',
      transports: ['websocket', 'polling'],
      reconnection: true,
      reconnectionAttempts: Infinity,
      reconnectionDelay: 1000,
      reconnectionDelayMax: 10000,
    });

    this.socket.on('connect', () => {
      console.log('[WS] Connected to entities namespace');
      this.connectionStatusSubject.next('connected');
    });

    this.socket.on('disconnect', (reason: string) => {
      console.log('[WS] Disconnected:', reason);
      this.connectionStatusSubject.next('disconnected');
    });

    this.socket.on('reconnecting', () => {
      this.connectionStatusSubject.next('reconnecting');
    });

    this.socket.on('reconnect_attempt', () => {
      this.connectionStatusSubject.next('reconnecting');
    });

    this.socket.on('connect_error', (error: Error) => {
      console.error('[WS] Connection error:', error.message);
      this.connectionStatusSubject.next('reconnecting');
    });

    // Entity events
    this.socket.on('entity:created', (entity: EntityEvent) => {
      this.entityUpdatesSubject.next({ ...entity, type: 'created' });
    });

    this.socket.on('entity:updated', (entity: EntityEvent) => {
      this.entityUpdatesSubject.next({ ...entity, type: 'updated' });
    });

    this.socket.on('entity:deleted', (entity: EntityEvent) => {
      this.entityUpdatesSubject.next({ ...entity, type: 'deleted' });
    });

    this.socket.on('entity:update', (event: EntityEvent) => {
      this.entityUpdatesSubject.next(event);
    });

    // Batched entity events (coalesced server-side for performance)
    this.socket.on('entity:batch', (events: EntityEvent[]) => {
      console.debug(`[WS] Received entity:batch with ${events.length} events`, events[0]);
      for (const event of events) {
        this.entityUpdatesSubject.next(event);
      }
    });

    // Ageout events (broadcast to all clients, not viewport-filtered)
    this.socket.on('events.entity.stale', (payload: any) => {
      this.ageoutSubject.next({ eventType: 'stale', payload });
    });

    this.socket.on('events.entity.agedout', (payload: any) => {
      this.ageoutSubject.next({ eventType: 'agedout', payload });
    });

    this.socket.on('events.entity.restored', (payload: any) => {
      this.ageoutSubject.next({ eventType: 'restored', payload });
    });

    // Alert events
    this.socket.on('alert:new', (alert: Alert) => {
      this.alertSubject.next(alert);
    });
  }

  sendViewportUpdate(bounds: { north: number; south: number; east: number; west: number }): void {
    if (this.socket?.connected) {
      this.socket.emit('viewport:update', bounds);
    }
  }

  subscribeToEntity(entityId: string): void {
    if (this.socket?.connected) {
      this.socket.emit('entity:subscribe', { entityId });
    }
  }

  unsubscribeFromEntity(entityId: string): void {
    if (this.socket?.connected) {
      this.socket.emit('entity:unsubscribe', { entityId });
    }
  }

  disconnect(): void {
    if (this.socket) {
      this.socket.removeAllListeners();
      this.socket.disconnect();
      this.socket = null;
      this.connectionStatusSubject.next('disconnected');
    }
  }

  ngOnDestroy(): void {
    this.disconnect();
  }
}
