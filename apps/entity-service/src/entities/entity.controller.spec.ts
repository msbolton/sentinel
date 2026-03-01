import { Test, TestingModule } from '@nestjs/testing';
import { EntityController } from './entity.controller';
import { EntityService } from './entity.service';

describe('EntityController', () => {
  let controller: EntityController;
  let entityService: {
    findWithinBoundingBox: jest.Mock;
    findNearby: jest.Mock;
    getEntityCount: jest.Mock;
    findById: jest.Mock;
    create: jest.Mock;
    update: jest.Mock;
    updatePosition: jest.Mock;
    delete: jest.Mock;
  };

  beforeEach(async () => {
    entityService = {
      findWithinBoundingBox: jest.fn(),
      findNearby: jest.fn(),
      getEntityCount: jest.fn(),
      findById: jest.fn(),
      create: jest.fn(),
      update: jest.fn(),
      updatePosition: jest.fn(),
      delete: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      controllers: [EntityController],
      providers: [
        { provide: EntityService, useValue: entityService },
      ],
    }).compile();

    controller = module.get<EntityController>(EntityController);
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  // ─── GET /entities (findAll / bounding box) ───────────────────────────

  describe('findAll', () => {
    it('should call entityService.findWithinBoundingBox with the query dto', async () => {
      const query = {
        north: 40,
        south: 38,
        east: -76,
        west: -78,
        page: 1,
        pageSize: 100,
      };
      const mockResult = {
        data: [{ id: 'uuid-1', name: 'Entity A' }],
        total: 1,
        page: 1,
        pageSize: 100,
      };
      entityService.findWithinBoundingBox.mockResolvedValue(mockResult);

      const result = await controller.findAll(query as any);

      expect(entityService.findWithinBoundingBox).toHaveBeenCalledWith(query);
      expect(result).toEqual(mockResult);
    });
  });

  // ─── GET /entities/nearby ─────────────────────────────────────────────

  describe('findNearby', () => {
    it('should call entityService.findNearby with lat, lng, and radius', async () => {
      const query = { lat: 38.9, lng: -77.0, radius: 5000 };
      const mockResult = [
        { id: 'uuid-2', name: 'Nearby Entity', distance: 1234 },
      ];
      entityService.findNearby.mockResolvedValue(mockResult);

      const result = await controller.findNearby(query as any);

      expect(entityService.findNearby).toHaveBeenCalledWith(38.9, -77.0, 5000);
      expect(result).toEqual(mockResult);
    });

    it('should default radius to 10000 when not provided', async () => {
      const query = { lat: 38.9, lng: -77.0 };
      entityService.findNearby.mockResolvedValue([]);

      await controller.findNearby(query as any);

      expect(entityService.findNearby).toHaveBeenCalledWith(38.9, -77.0, 10000);
    });
  });

  // ─── GET /entities/counts ─────────────────────────────────────────────

  describe('getCounts', () => {
    it('should call entityService.getEntityCount', async () => {
      const mockCounts = [
        { entityType: 'VESSEL', count: 42 },
        { entityType: 'AIRCRAFT', count: 17 },
      ];
      entityService.getEntityCount.mockResolvedValue(mockCounts);

      const result = await controller.getCounts();

      expect(entityService.getEntityCount).toHaveBeenCalled();
      expect(result).toEqual(mockCounts);
    });
  });

  // ─── GET /entities/:id ────────────────────────────────────────────────

  describe('findOne', () => {
    it('should call entityService.findById with the id', async () => {
      const mockEntity = { id: 'uuid-1', name: 'Test Entity', entityType: 'VESSEL' };
      entityService.findById.mockResolvedValue(mockEntity);

      const result = await controller.findOne('uuid-1');

      expect(entityService.findById).toHaveBeenCalledWith('uuid-1');
      expect(result).toEqual(mockEntity);
    });
  });

  // ─── POST /entities ───────────────────────────────────────────────────

  describe('create', () => {
    it('should call entityService.create with the dto', async () => {
      const dto = {
        name: 'New Entity',
        entityType: 'VESSEL',
        source: 'AIS',
        classification: 'UNCLASSIFIED',
      };
      const mockCreated = { id: 'uuid-new', ...dto };
      entityService.create.mockResolvedValue(mockCreated);

      const result = await controller.create(dto as any);

      expect(entityService.create).toHaveBeenCalledWith(dto);
      expect(result).toEqual(mockCreated);
    });
  });

  // ─── PATCH /entities/:id ──────────────────────────────────────────────

  describe('update', () => {
    it('should call entityService.update with id and dto', async () => {
      const dto = { name: 'Updated Entity' };
      const mockUpdated = { id: 'uuid-1', name: 'Updated Entity', entityType: 'VESSEL' };
      entityService.update.mockResolvedValue(mockUpdated);

      const result = await controller.update('uuid-1', dto as any);

      expect(entityService.update).toHaveBeenCalledWith('uuid-1', dto);
      expect(result).toEqual(mockUpdated);
    });
  });

  // ─── PATCH /entities/:id/position ─────────────────────────────────────

  describe('updatePosition', () => {
    it('should call entityService.updatePosition with id and dto', async () => {
      const dto = { lat: 39.0, lng: -77.5, heading: 180, speedKnots: 12 };
      const mockUpdated = {
        id: 'uuid-1',
        name: 'Test Entity',
        position: { type: 'Point', coordinates: [-77.5, 39.0] },
      };
      entityService.updatePosition.mockResolvedValue(mockUpdated);

      const result = await controller.updatePosition('uuid-1', dto as any);

      expect(entityService.updatePosition).toHaveBeenCalledWith('uuid-1', dto);
      expect(result).toEqual(mockUpdated);
    });
  });

  // ─── DELETE /entities/:id ─────────────────────────────────────────────

  describe('delete', () => {
    it('should call entityService.delete with the id', async () => {
      entityService.delete.mockResolvedValue(undefined);

      const result = await controller.delete('uuid-1');

      expect(entityService.delete).toHaveBeenCalledWith('uuid-1');
      expect(result).toBeUndefined();
    });
  });
});
