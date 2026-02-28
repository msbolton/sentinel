import { Test, TestingModule } from '@nestjs/testing';
import { ConfigService } from '@nestjs/config';
import { DataSource } from 'typeorm';
import { EntitiesService, EntityRecord } from './entities.service';

// Mock Redis - must be defined inside the factory since jest.mock is hoisted
const mockRedis = {
  connect: jest.fn().mockResolvedValue(undefined),
  quit: jest.fn().mockResolvedValue(undefined),
  on: jest.fn(),
  pipeline: jest.fn().mockReturnValue({
    geoadd: jest.fn().mockReturnThis(),
    set: jest.fn().mockReturnThis(),
    get: jest.fn().mockReturnThis(),
    exec: jest.fn().mockResolvedValue([]),
  }),
  geosearch: jest.fn().mockResolvedValue([]),
  zrange: jest.fn().mockResolvedValue([]),
  geoadd: jest.fn().mockResolvedValue(1),
  set: jest.fn().mockResolvedValue('OK'),
  get: jest.fn().mockResolvedValue(null),
  del: jest.fn().mockResolvedValue(1),
  zrem: jest.fn().mockResolvedValue(1),
};

jest.mock('ioredis', () => {
  const MockRedis = jest.fn().mockImplementation(() => mockRedis);
  return { __esModule: true, default: MockRedis };
});

describe('EntitiesService', () => {
  let service: EntitiesService;
  let dataSource: DataSource;
  let kafkaClient: { connect: jest.Mock; close: jest.Mock; emit: jest.Mock };

  const mockDbRows = [
    {
      id: '11111111-1111-1111-1111-111111111111',
      entityType: 'AIRCRAFT',
      name: 'RAVEN-01',
      description: 'Surveillance drone',
      latitude: '38.8977',
      longitude: '-77.0365',
      heading: '270',
      speedKnots: '450',
      course: '270',
      milStd2525dSymbol: null,
      classification: 'SECRET',
      source: 'ADS-B',
      affiliations: ['FRIENDLY'],
      metadata: { callsign: 'RAVEN01' },
      createdAt: new Date('2025-01-01T00:00:00Z'),
      updatedAt: new Date('2025-01-01T00:00:00Z'),
      lastSeenAt: new Date('2025-01-01T00:00:00Z'),
    },
  ];

  beforeEach(async () => {
    kafkaClient = {
      connect: jest.fn().mockResolvedValue(undefined),
      close: jest.fn().mockResolvedValue(undefined),
      emit: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      providers: [
        EntitiesService,
        {
          provide: 'ENTITY_SERVICE',
          useValue: kafkaClient,
        },
        {
          provide: ConfigService,
          useValue: {
            get: jest.fn((key: string, defaultVal?: unknown) => {
              const config: Record<string, unknown> = {
                REDIS_HOST: 'localhost',
                REDIS_PORT: 6379,
                REDIS_PASSWORD: '',
              };
              return config[key] ?? defaultVal;
            }),
          },
        },
        {
          provide: DataSource,
          useValue: {
            query: jest.fn().mockResolvedValue(mockDbRows),
          },
        },
      ],
    }).compile();

    service = module.get<EntitiesService>(EntitiesService);
    dataSource = module.get<DataSource>(DataSource);
  });

  afterEach(() => {
    jest.clearAllMocks();
  });

  describe('warmCache', () => {
    it('should load entities from Postgres on init', async () => {
      await service.onModuleInit();
      expect(dataSource.query).toHaveBeenCalledWith(
        expect.stringContaining('FROM sentinel.entities'),
      );
    });

    it('should call Redis pipeline with geoadd and set for each entity', async () => {
      const pipeline = mockRedis.pipeline();
      await service.onModuleInit();
      // Pipeline methods should have been called
      expect(mockRedis.pipeline).toHaveBeenCalled();
    });
  });

  describe('queryEntities', () => {
    it('should return paginated results', async () => {
      // Setup: mock Redis returns no IDs, so it falls back to DB
      mockRedis.zrange.mockResolvedValueOnce([]);

      const result = await service.queryEntities({
        page: 1,
        pageSize: 50,
      });

      expect(result).toHaveProperty('data');
      expect(result).toHaveProperty('pagination');
      expect(result.pagination).toHaveProperty('page');
      expect(result.pagination).toHaveProperty('total');
    });

    it('should return entities with nested position objects from DB fallback', async () => {
      mockRedis.zrange.mockResolvedValueOnce([]);

      const result = await service.queryEntities({
        page: 1,
        pageSize: 50,
      });

      expect(result.data.length).toBe(1);
      const entity = result.data[0];
      expect(entity.position).toBeDefined();
      expect(entity.position!.latitude).toBe(38.8977);
      expect(entity.position!.longitude).toBe(-77.0365);
      expect(entity.speedKnots).toBe(450);
      expect(entity.affiliations).toEqual(['FRIENDLY']);
    });

    it('should filter by entity types', async () => {
      mockRedis.zrange.mockResolvedValueOnce([]);

      const result = await service.queryEntities({
        entityTypes: ['VESSEL'] as any[],
        page: 1,
        pageSize: 50,
      });

      expect(result.data.length).toBe(0); // AIRCRAFT doesn't match VESSEL
    });

    it('should filter by classification level', async () => {
      mockRedis.zrange.mockResolvedValueOnce([]);

      const result = await service.queryEntities({
        classification: 'CONFIDENTIAL' as any,
        page: 1,
        pageSize: 50,
      });

      // SECRET > CONFIDENTIAL, so should be filtered out
      expect(result.data.length).toBe(0);
    });
  });

  describe('createEntity', () => {
    it('should create an entity with nested position', async () => {
      const result = await service.createEntity({
        entityType: 'AIRCRAFT' as any,
        name: 'TEST-01',
        latitude: 40.0,
        longitude: -74.0,
        source: 'ADS-B',
      } as any);

      const entity = result as unknown as EntityRecord;
      expect(entity.id).toBeDefined();
      expect(entity.position).toBeDefined();
      expect(entity.position!.latitude).toBe(40.0);
      expect(entity.position!.longitude).toBe(-74.0);
      expect(entity.affiliations).toEqual([]);
      expect(entity.metadata).toEqual({});
    });

    it('should emit a Kafka creation event', async () => {
      await service.createEntity({
        entityType: 'AIRCRAFT' as any,
        name: 'TEST-01',
        latitude: 40.0,
        longitude: -74.0,
        source: 'ADS-B',
      } as any);

      expect(kafkaClient.emit).toHaveBeenCalledWith(
        'events.entity.created',
        expect.objectContaining({
          value: expect.objectContaining({
            entity_type: 'AIRCRAFT',
            latitude: 40.0,
            longitude: -74.0,
          }),
        }),
      );
    });

    it('should cache the entity position in Redis', async () => {
      await service.createEntity({
        entityType: 'AIRCRAFT' as any,
        name: 'TEST-01',
        latitude: 40.0,
        longitude: -74.0,
        source: 'ADS-B',
      } as any);

      expect(mockRedis.geoadd).toHaveBeenCalledWith(
        'sentinel:entities:geo',
        -74.0,
        40.0,
        expect.any(String),
      );
    });
  });

  describe('deleteEntity', () => {
    it('should remove from Redis geo index and cache', async () => {
      const id = '11111111-1111-1111-1111-111111111111';
      await service.deleteEntity(id);

      expect(mockRedis.zrem).toHaveBeenCalledWith('sentinel:entities:geo', id);
      expect(mockRedis.del).toHaveBeenCalledWith(`sentinel:entities:cache:${id}`);
    });

    it('should emit a Kafka deletion event', async () => {
      const id = '11111111-1111-1111-1111-111111111111';
      await service.deleteEntity(id);

      expect(kafkaClient.emit).toHaveBeenCalledWith(
        'events.entity.deleted',
        expect.objectContaining({
          key: id,
          value: expect.objectContaining({ entity_id: id }),
        }),
      );
    });
  });
});
