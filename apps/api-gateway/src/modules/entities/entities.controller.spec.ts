import { Test, TestingModule } from '@nestjs/testing';
import { EntitiesController } from './entities.controller';
import { EntitiesService, EntityRecord, PaginatedEntitiesResult } from './entities.service';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { Reflector } from '@nestjs/core';

describe('EntitiesController', () => {
  let controller: EntitiesController;
  let service: jest.Mocked<EntitiesService>;

  const mockEntity: EntityRecord = {
    id: '11111111-1111-1111-1111-111111111111',
    entityType: 'AIRCRAFT',
    name: 'RAVEN-01',
    description: 'Surveillance drone',
    source: 'ADS-B',
    classification: 'SECRET',
    position: { latitude: 38.8977, longitude: -77.0365 },
    heading: 270,
    speedKnots: 450,
    metadata: { callsign: 'RAVEN01' },
    affiliations: ['FRIENDLY'],
    createdAt: '2025-01-01T00:00:00.000Z',
    updatedAt: '2025-01-01T00:00:00.000Z',
  };

  const mockPaginatedResult: PaginatedEntitiesResult = {
    data: [mockEntity],
    pagination: { page: 1, pageSize: 50, total: 1, totalPages: 1 },
  };

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      controllers: [EntitiesController],
      providers: [
        {
          provide: EntitiesService,
          useValue: {
            queryEntities: jest.fn().mockResolvedValue(mockPaginatedResult),
            getEntityById: jest.fn().mockResolvedValue(mockEntity),
            createEntity: jest.fn().mockResolvedValue(mockEntity),
            updateEntity: jest.fn().mockResolvedValue(mockEntity),
            deleteEntity: jest.fn().mockResolvedValue(undefined),
          },
        },
        Reflector,
      ],
    })
      .overrideGuard(JwtAuthGuard)
      .useValue({ canActivate: () => true })
      .compile();

    controller = module.get<EntitiesController>(EntitiesController);
    service = module.get(EntitiesService);
  });

  describe('GET /entities', () => {
    it('should return paginated entities', async () => {
      const result = await controller.queryEntities({ page: 1, pageSize: 50 });

      expect(result).toEqual(mockPaginatedResult);
      expect(service.queryEntities).toHaveBeenCalledWith({ page: 1, pageSize: 50 });
    });

    it('should pass bounding box parameters to service', async () => {
      const query = { north: 40, south: 38, east: -76, west: -78, page: 1, pageSize: 50 };
      await controller.queryEntities(query);

      expect(service.queryEntities).toHaveBeenCalledWith(query);
    });

    it('should return entities with nested position in response', async () => {
      const result = await controller.queryEntities({ page: 1, pageSize: 50 });

      expect(result.data[0].position).toEqual({
        latitude: 38.8977,
        longitude: -77.0365,
      });
      expect(result.data[0].speedKnots).toBe(450);
      expect(result.data[0].affiliations).toEqual(['FRIENDLY']);
    });
  });

  describe('GET /entities/:id', () => {
    it('should return a single entity by ID', async () => {
      const result = await controller.getEntityById('11111111-1111-1111-1111-111111111111');

      expect(result).toEqual(mockEntity);
      expect(service.getEntityById).toHaveBeenCalledWith('11111111-1111-1111-1111-111111111111');
    });
  });

  describe('POST /entities', () => {
    it('should create a new entity', async () => {
      const dto = {
        entityType: 'AIRCRAFT' as any,
        name: 'TEST-01',
        latitude: 40.0,
        longitude: -74.0,
        source: 'ADS-B',
      };

      const result = await controller.createEntity(dto as any);

      expect(result).toEqual(mockEntity);
      expect(service.createEntity).toHaveBeenCalledWith(dto);
    });
  });

  describe('PUT /entities/:id', () => {
    it('should update an existing entity', async () => {
      const dto = { name: 'RAVEN-02' };
      const result = await controller.updateEntity(
        '11111111-1111-1111-1111-111111111111',
        dto as any,
      );

      expect(result).toEqual(mockEntity);
      expect(service.updateEntity).toHaveBeenCalledWith(
        '11111111-1111-1111-1111-111111111111',
        dto,
      );
    });
  });

  describe('DELETE /entities/:id', () => {
    it('should delete an entity', async () => {
      await controller.deleteEntity('11111111-1111-1111-1111-111111111111');

      expect(service.deleteEntity).toHaveBeenCalledWith('11111111-1111-1111-1111-111111111111');
    });
  });
});
