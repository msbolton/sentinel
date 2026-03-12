import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { FederationService } from './federation.service';
import { WebSocketService, FederationStatusEvent, PresenceUpdateEvent } from './websocket.service';
import { Subject } from 'rxjs';

describe('FederationService', () => {
  let service: FederationService;
  let httpMock: HttpTestingController;
  const federationStatusSubject = new Subject<FederationStatusEvent>();
  const presenceUpdateSubject = new Subject<PresenceUpdateEvent>();

  const mockWsService = {
    federationStatus$: federationStatusSubject.asObservable(),
    presenceUpdates$: presenceUpdateSubject.asObservable(),
  };

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        FederationService,
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: WebSocketService, useValue: mockWsService },
      ],
    });

    service = TestBed.inject(FederationService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('should be created', () => {
    expect(service).toBeTruthy();
  });

  it('should update peers when federation status arrives', () => {
    const statusEvent: FederationStatusEvent = {
      peers: [{
        instanceId: 'peer-1',
        displayName: 'Bravo',
        status: 'connected',
        color: '#f97316',
        entityCount: 47,
        userCount: 2,
      }],
    };

    federationStatusSubject.next(statusEvent);
    expect(service.peers().length).toBe(1);
    expect(service.peers()[0].displayName).toBe('Bravo');
  });

  it('should update presence entries when presence event arrives', () => {
    const presenceEvent: PresenceUpdateEvent = {
      users: [{
        userId: 'u1',
        displayName: 'j.smith',
        instanceId: 'peer-1',
        instanceName: 'Bravo',
        cameraCenter: { lat: 34.05, lon: -118.25 },
        zoom: 8,
        timestamp: Date.now(),
        color: '#f97316',
      }],
    };

    presenceUpdateSubject.next(presenceEvent);
    expect(service.presenceEntries().length).toBe(1);
  });

  it('should expire presence entries after 5s', () => {
    jest.useFakeTimers();
    // Recreate service so its setInterval uses fake timers
    TestBed.resetTestingModule();
    TestBed.configureTestingModule({
      providers: [
        FederationService,
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: WebSocketService, useValue: mockWsService },
      ],
    });
    const timerService = TestBed.inject(FederationService);

    const presenceEvent: PresenceUpdateEvent = {
      users: [{
        userId: 'u1',
        displayName: 'j.smith',
        instanceId: 'peer-1',
        instanceName: 'Bravo',
        cameraCenter: { lat: 34.05, lon: -118.25 },
        zoom: 8,
        timestamp: Date.now() - 6000, // 6 seconds ago
        color: '#f97316',
      }],
    };

    presenceUpdateSubject.next(presenceEvent);
    jest.advanceTimersByTime(1000); // trigger cleanup
    expect(timerService.presenceEntries().length).toBe(0);
    jest.useRealTimers();
  });

  it('should track federation enabled state', () => {
    expect(service.federationActive()).toBe(false);

    federationStatusSubject.next({
      peers: [{
        instanceId: 'peer-1',
        displayName: 'Bravo',
        status: 'connected',
        color: '#f97316',
        entityCount: 10,
        userCount: 1,
      }],
    });

    expect(service.federationActive()).toBe(true);
  });
});
