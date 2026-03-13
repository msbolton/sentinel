import { Test, TestingModule } from '@nestjs/testing';
import { EventEmitterModule, EventEmitter2 } from '@nestjs/event-emitter';
import { FederationBridgeService } from './federation-bridge.service';
import { EntityGateway } from './entity.gateway';
import { PeerManagerService } from '../federation/peer-manager.service';

describe('FederationBridgeService', () => {
  let service: FederationBridgeService;
  let eventEmitter: EventEmitter2;
  let mockEntityGateway: Partial<EntityGateway>;

  beforeEach(async () => {
    mockEntityGateway = {
      broadcastEntityBatch: jest.fn().mockResolvedValue(undefined),
      server: { emit: jest.fn() } as any,
    };

    const mockPeerManager = {
      getConnectedPeers: jest.fn().mockReturnValue([
        { instanceId: 'peer-1', displayName: 'Bravo', ceiling: 'classification-u', color: '#f97316' },
      ]),
    };

    const module: TestingModule = await Test.createTestingModule({
      imports: [EventEmitterModule.forRoot()],
      providers: [
        FederationBridgeService,
        { provide: EntityGateway, useValue: mockEntityGateway },
        { provide: PeerManagerService, useValue: mockPeerManager },
      ],
    }).compile();

    await module.init();

    service = module.get(FederationBridgeService);
    eventEmitter = module.get(EventEmitter2);
  });

  it('should be defined', () => {
    expect(service).toBeDefined();
  });

  it('should forward entity batch to gateway on federation event', async () => {
    const message = {
      type: 'fed:entity:batch',
      sourceInstanceId: 'peer-1',
      classificationLevel: 'classification-u',
      payload: {
        entities: [{
          entityId: 'ent-1', entityType: 'AIRCRAFT', latitude: 34.05, longitude: -118.25,
          classification: 'UNCLASSIFIED', source: 'peer-radar', timestamp: new Date().toISOString(),
          sourceInstanceId: 'peer-1', sourceInstanceName: 'Bravo',
        }],
      },
    };
    await eventEmitter.emitAsync('federation.fed:entity:batch', message);
    expect(mockEntityGateway.broadcastEntityBatch).toHaveBeenCalledWith(
      expect.arrayContaining([
        expect.objectContaining({
          entity: expect.objectContaining({ entityId: 'ent-1', sourceInstanceId: 'peer-1', sourceInstanceName: 'Bravo' }),
          eventType: 'updated',
        }),
      ]),
    );
  });

  it('should forward presence to browser clients with peer metadata', async () => {
    const message = {
      type: 'fed:presence:update', sourceInstanceId: 'peer-1', classificationLevel: 'classification-u',
      payload: { users: [{ userId: 'u1', displayName: 'j.smith', cameraCenter: { lat: 34.05, lon: -118.25 }, zoom: 8, timestamp: Date.now() }] },
    };
    await eventEmitter.emitAsync('federation.fed:presence:update', message);
    expect(mockEntityGateway.server!.emit).toHaveBeenCalledWith('federation:presence',
      expect.objectContaining({ users: expect.arrayContaining([expect.objectContaining({ userId: 'u1', instanceId: 'peer-1', instanceName: 'Bravo', color: '#f97316' })]) }),
    );
  });

  it('should broadcast federation status on peer connect', async () => {
    await eventEmitter.emitAsync('federation.peer.connected', { instanceId: 'peer-1', displayName: 'Bravo', ceiling: 'classification-u' });
    expect(mockEntityGateway.server!.emit).toHaveBeenCalledWith('federation:status',
      expect.objectContaining({ peers: expect.arrayContaining([expect.objectContaining({ instanceId: 'peer-1', displayName: 'Bravo', color: '#f97316' })]) }),
    );
  });

  it('should broadcast federation status on peer disconnect', async () => {
    await eventEmitter.emitAsync('federation.peer.disconnected', { instanceId: 'peer-1' });
    expect(mockEntityGateway.server!.emit).toHaveBeenCalledWith('federation:status', expect.objectContaining({ peers: expect.any(Array) }));
  });
});
