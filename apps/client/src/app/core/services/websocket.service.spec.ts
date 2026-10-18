import { TestBed } from '@angular/core/testing';
import { WebSocketService, ConnectionStatus } from './websocket.service';
import { AuthService } from './auth.service';
import { EntityEvent } from '../../shared/models/entity.model';

class MockWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: MockWebSocket[] = [];

  readyState = MockWebSocket.CONNECTING;
  sent: string[] = [];
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;

  constructor(public readonly url: string) {
    MockWebSocket.instances.push(this);
  }

  send(data: string): void {
    this.sent.push(data);
  }

  close(): void {
    this.readyState = MockWebSocket.CLOSED;
  }

  // Test helpers
  simulateOpen(): void {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.(new Event('open'));
  }

  simulateServerClose(): void {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.({ code: 1006, reason: '' } as CloseEvent);
  }

  receive(frame: unknown): void {
    this.onmessage?.({ data: JSON.stringify(frame) } as MessageEvent);
  }

  frames(): Array<{ type: string; data: unknown }> {
    return this.sent.map((s) => JSON.parse(s));
  }
}

describe('WebSocketService', () => {
  let service: WebSocketService;
  const originalWebSocket = (globalThis as any).WebSocket;
  let token: string | null;

  beforeEach(() => {
    MockWebSocket.instances = [];
    token = 'test-token';
    (globalThis as any).WebSocket = MockWebSocket;
    TestBed.configureTestingModule({
      providers: [{ provide: AuthService, useValue: { getToken: () => token } }],
    });
    service = TestBed.inject(WebSocketService);
  });

  afterEach(() => {
    service.disconnect();
    (globalThis as any).WebSocket = originalWebSocket;
    jest.useRealTimers();
  });

  function latestSocket(): MockWebSocket {
    return MockWebSocket.instances[MockWebSocket.instances.length - 1];
  }

  it('connects to /ws/entities on the current host with the bearer token', () => {
    service.connect();
    const socket = latestSocket();
    expect(socket.url).toMatch(/^ws:\/\/.+\/ws\/entities\?token=test-token$/);
  });

  it('omits the token parameter when no token is available', () => {
    token = null;
    service.connect();
    expect(latestSocket().url).toMatch(/\/ws\/entities$/);
  });

  it('does not open a second socket when connect is called twice', () => {
    service.connect();
    service.connect();
    expect(MockWebSocket.instances.length).toBe(1);
  });

  it('reports connected on open and replays viewport and subscriptions', () => {
    const statuses: ConnectionStatus[] = [];
    service.connectionStatus$.subscribe((s) => statuses.push(s));

    service.sendViewportUpdate({ north: 10, south: 0, east: 10, west: 0 });
    service.subscribeToEntity('e-1');
    service.connect();
    const socket = latestSocket();
    expect(socket.sent.length).toBe(0); // nothing sent before open

    socket.simulateOpen();
    expect(statuses).toEqual(['disconnected', 'connected']);
    expect(socket.frames()).toEqual([
      { type: 'viewport:update', data: { north: 10, south: 0, east: 10, west: 0 } },
      { type: 'entity:subscribe', data: { entityId: 'e-1' } },
    ]);
  });

  it('sends viewport updates and subscription changes while open', () => {
    service.connect();
    const socket = latestSocket();
    socket.simulateOpen();
    service.sendViewportUpdate({ north: 1, south: -1, east: 1, west: -1 });
    service.unsubscribeFromEntity('e-9');
    const frames = socket.frames();
    expect(frames[frames.length - 2]).toEqual({ type: 'viewport:update', data: { north: 1, south: -1, east: 1, west: -1 } });
    expect(frames[frames.length - 1]).toEqual({ type: 'entity:unsubscribe', data: { entityId: 'e-9' } });
  });

  it('fans out single and batched ageout frames as one event per payload', () => {
    const received: Array<{ eventType: string; payload: any }> = [];
    service.ageoutEvents$.subscribe((e) => received.push(e));
    service.connect();
    const socket = latestSocket();
    socket.simulateOpen();

    socket.receive({ type: 'events.entity.stale', data: { entity_id: 'a' } });
    socket.receive({ type: 'events.entity.agedout', data: [{ entity_id: 'b' }, { entity_id: 'c' }] });
    socket.receive({ type: 'events.entity.restored', data: [] });
    socket.receive({ type: 'events.entity.restored', data: null });

    expect(received).toEqual([
      { eventType: 'stale', payload: { entity_id: 'a' } },
      { eventType: 'agedout', payload: { entity_id: 'b' } },
      { eventType: 'agedout', payload: { entity_id: 'c' } },
    ]);
  });

  it('derives the server clock skew from connection:ack', () => {
    jest.useFakeTimers().setSystemTime(new Date('2026-10-14T00:00:00Z'));
    service.connect();
    const socket = latestSocket();
    socket.simulateOpen();
    expect(service.serverNow()).toBe(Date.parse('2026-10-14T00:00:00Z'));

    socket.receive({ type: 'connection:ack', data: { clientId: 'x', serverTime: '2026-10-14T00:00:30Z' } });
    expect(service.serverNow()).toBe(Date.parse('2026-10-14T00:00:30Z'));

    // A malformed server time leaves the skew untouched.
    socket.receive({ type: 'connection:ack', data: { clientId: 'x', serverTime: 'nope' } });
    expect(service.serverNow()).toBe(Date.parse('2026-10-14T00:00:30Z'));
  });

  it('fans out entity:batch frames as individual entity events', () => {
    const received: EntityEvent[] = [];
    service.entityUpdates$.subscribe((e) => received.push(e));
    service.connect();
    const socket = latestSocket();
    socket.simulateOpen();

    const event = (id: string): EntityEvent => ({
      type: 'updated',
      entity: { id, entityType: 'AIRCRAFT', source: 'ADS_B', classification: 'UNCLASSIFIED', metadata: {}, affiliations: [], createdAt: 't', updatedAt: 't' } as any,
      timestamp: 't',
    });
    socket.receive({ type: 'entity:batch', data: [event('a'), event('b')] });
    socket.receive({ type: 'entity:deleted', data: { entity: { id: 'c' }, timestamp: 't' } });

    expect(received.map((e) => e.entity.id)).toEqual(['a', 'b', 'c']);
    expect(received[2].type).toBe('deleted');
  });

  it('routes ageout and alert frames to their streams', () => {
    const ageout: Array<{ eventType: string; payload: any }> = [];
    const alerts: any[] = [];
    service.ageoutEvents$.subscribe((e) => ageout.push(e));
    service.alertStream$.subscribe((a) => alerts.push(a));
    service.connect();
    const socket = latestSocket();
    socket.simulateOpen();

    socket.receive({ type: 'events.entity.stale', data: { entity_id: 'x' } });
    socket.receive({ type: 'events.entity.agedout', data: { entity_id: 'y' } });
    socket.receive({ type: 'events.entity.restored', data: { entity_id: 'z' } });
    socket.receive({ type: 'alert:new', data: { id: 'a1', title: 'Breach' } });
    socket.receive({ type: 'connection:ack', data: { clientId: '1' } });
    socket.onmessage?.({ data: 'not json' } as MessageEvent);

    expect(ageout.map((e) => e.eventType)).toEqual(['stale', 'agedout', 'restored']);
    expect(alerts).toEqual([{ id: 'a1', title: 'Breach' }]);
  });

  it('routes density grids to their stream', () => {
    const grids: any[] = [];
    service.density$.subscribe((g) => grids.push(g));
    service.connect();
    const socket = latestSocket();
    socket.simulateOpen();

    const grid = { feedId: 'aisstream', entityType: 'VESSEL', capSquareDegrees: 2500, cells: [[50.5, -1.5, 2]] };
    socket.receive({ type: 'density:grid', data: grid });
    socket.receive({ type: 'density:grid' });

    expect(grids).toEqual([grid]);
  });

  it('reconnects with backoff after an unexpected close and replays state', () => {
    jest.useFakeTimers();
    const statuses: ConnectionStatus[] = [];
    service.connectionStatus$.subscribe((s) => statuses.push(s));

    service.connect();
    const first = latestSocket();
    first.simulateOpen();
    service.sendViewportUpdate({ north: 5, south: 0, east: 5, west: 0 });
    first.simulateServerClose();

    expect(statuses[statuses.length - 1]).toBe('reconnecting');
    expect(MockWebSocket.instances.length).toBe(1);

    jest.advanceTimersByTime(1300); // first backoff is 1s plus jitter
    expect(MockWebSocket.instances.length).toBe(2);
    const second = latestSocket();
    second.simulateOpen();
    expect(statuses[statuses.length - 1]).toBe('connected');
    expect(second.frames()[0]).toEqual({ type: 'viewport:update', data: { north: 5, south: 0, east: 5, west: 0 } });
  });

  it('does not reconnect after an intentional disconnect', () => {
    jest.useFakeTimers();
    service.connect();
    const socket = latestSocket();
    socket.simulateOpen();
    service.disconnect();
    jest.advanceTimersByTime(20000);
    expect(MockWebSocket.instances.length).toBe(1);
    expect(socket.readyState).toBe(MockWebSocket.CLOSED);
  });
});
