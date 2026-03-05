import { Test, TestingModule } from '@nestjs/testing';
import { IngestConsumer } from './ingest.consumer';
import { EntityService } from './entity.service';
import { EntityRepository } from './entity.repository';
import { EntityType, EntitySource, Classification } from './enums';
import { DataSource, SelectQueryBuilder } from 'typeorm';

describe('IngestConsumer', () => {
  let consumer: IngestConsumer;
  let entityService: {
    create: jest.Mock;
    update: jest.Mock;
    updatePosition: jest.Mock;
  };
  let entityRepository: {
    createQueryBuilder: jest.Mock;
  };
  let dataSource: {
    query: jest.Mock;
  };

  let mockQueryBuilder: {
    select: jest.Mock;
    where: jest.Mock;
    andWhere: jest.Mock;
    getOne: jest.Mock;
  };

  beforeEach(async () => {
    entityService = {
      create: jest.fn().mockResolvedValue({ id: 'new-uuid' }),
      update: jest.fn().mockResolvedValue({}),
      updatePosition: jest.fn().mockResolvedValue({}),
    };

    mockQueryBuilder = {
      select: jest.fn().mockReturnThis(),
      where: jest.fn().mockReturnThis(),
      andWhere: jest.fn().mockReturnThis(),
      getOne: jest.fn().mockResolvedValue(null),
    };

    entityRepository = {
      createQueryBuilder: jest.fn().mockReturnValue(mockQueryBuilder),
    };

    dataSource = {
      query: jest.fn().mockResolvedValue(undefined),
    };

    const module: TestingModule = await Test.createTestingModule({
      controllers: [IngestConsumer],
      providers: [
        { provide: EntityService, useValue: entityService },
        { provide: EntityRepository, useValue: entityRepository },
        { provide: DataSource, useValue: dataSource },
      ],
    }).compile();

    consumer = module.get<IngestConsumer>(IngestConsumer);
  });

  it('should be defined', () => {
    expect(consumer).toBeDefined();
  });

  describe('onModuleInit', () => {
    it('should create the sourceEntityId index', async () => {
      await consumer.onModuleInit();
      expect(dataSource.query).toHaveBeenCalledWith(
        expect.stringContaining('idx_entities_source_entity_id'),
      );
    });
  });

  // ─── New entity creation ────────────────────────────────────────────

  describe('new ADS-B entity', () => {
    it('should create with AIRCRAFT type and ADS_B source', async () => {
      const message = {
        entity_id: 'ICAO-A1B2C3',
        entity_type: 'aircraft',
        name: 'BAW123',
        source: 'tcp',
        latitude: 51.5,
        longitude: -0.1,
        altitude: 10000,
        heading: 90,
        speed_knots: 450,
        course: 90,
        timestamp: '2025-01-15T12:00:00Z',
      };

      await consumer.handleIngestMessage(message);

      expect(entityService.create).toHaveBeenCalledWith(
        expect.objectContaining({
          entityType: EntityType.AIRCRAFT,
          source: EntitySource.ADS_B,
          name: 'BAW123',
          altitude: 10000,
          position: { lat: 51.5, lng: -0.1, altitude: 10000 },
          metadata: { sourceEntityId: 'ICAO-A1B2C3' },
        }),
      );
    });
  });

  // ─── Existing entity with position ─────────────────────────────────

  describe('existing entity with position', () => {
    it('should call updatePosition', async () => {
      mockQueryBuilder.getOne.mockResolvedValue({
        id: 'existing-uuid',
        name: 'MMSI 123456789',
      });

      const message = {
        entity_id: 'MMSI-123456789',
        entity_type: 'vessel',
        name: 'MMSI 123456789',
        source: 'tcp',
        latitude: 38.9,
        longitude: -77.0,
        altitude: 0,
        heading: 180,
        speed_knots: 12,
        course: 180,
        timestamp: '2025-01-15T12:00:00Z',
      };

      await consumer.handleIngestMessage(message);

      expect(entityService.updatePosition).toHaveBeenCalledWith(
        'existing-uuid',
        expect.objectContaining({
          lat: 38.9,
          lng: -77.0,
          heading: 180,
          speedKnots: 12,
          course: 180,
        }),
      );
      expect(entityService.create).not.toHaveBeenCalled();
    });
  });

  // ─── Existing entity with name change ──────────────────────────────

  describe('existing entity with name change', () => {
    it('should call update with new name', async () => {
      mockQueryBuilder.getOne.mockResolvedValue({
        id: 'existing-uuid',
        name: 'MMSI 123456789',
      });

      const message = {
        entity_id: 'MMSI-123456789',
        entity_type: 'vessel',
        name: 'EVER GIVEN',
        source: 'tcp',
        latitude: 0,
        longitude: 0,
        altitude: 0,
        heading: 0,
        speed_knots: 0,
        course: 0,
        timestamp: '2025-01-15T12:00:00Z',
      };

      await consumer.handleIngestMessage(message);

      expect(entityService.update).toHaveBeenCalledWith('existing-uuid', {
        name: 'EVER GIVEN',
      });
    });
  });

  // ─── AIS Type 5 (zero lat/lon) ────────────────────────────────────

  describe('AIS Type 5 with zero lat/lon', () => {
    it('should skip position update but still update name', async () => {
      mockQueryBuilder.getOne.mockResolvedValue({
        id: 'existing-uuid',
        name: 'MMSI 123456789',
      });

      const message = {
        entity_id: 'MMSI-123456789',
        entity_type: 'vessel',
        name: 'EVER GIVEN',
        source: 'tcp',
        latitude: 0,
        longitude: 0,
        altitude: 0,
        heading: 0,
        speed_knots: 0,
        course: 0,
        timestamp: '2025-01-15T12:00:00Z',
      };

      await consumer.handleIngestMessage(message);

      expect(entityService.updatePosition).not.toHaveBeenCalled();
      expect(entityService.update).toHaveBeenCalledWith('existing-uuid', {
        name: 'EVER GIVEN',
      });
    });
  });

  // ─── Entity type mapping ───────────────────────────────────────────

  describe('entity type mapping', () => {
    const cases: Array<[string, EntityType]> = [
      ['aircraft', EntityType.AIRCRAFT],
      ['vessel', EntityType.VESSEL],
      ['vehicle', EntityType.VEHICLE],
      ['person', EntityType.PERSON],
      ['drone', EntityType.DRONE],
      ['unknown', EntityType.UNKNOWN],
      ['sensor', EntityType.EQUIPMENT],
    ];

    it.each(cases)(
      'maps "%s" to %s',
      async (ingestType, expectedType) => {
        const message = {
          entity_id: 'TEST-1',
          entity_type: ingestType,
          name: 'Test',
          source: 'tcp',
          latitude: 10,
          longitude: 20,
          altitude: 0,
          heading: 0,
          speed_knots: 0,
          course: 0,
          timestamp: '2025-01-15T12:00:00Z',
        };

        await consumer.handleIngestMessage(message);

        expect(entityService.create).toHaveBeenCalledWith(
          expect.objectContaining({ entityType: expectedType }),
        );
      },
    );
  });

  // ─── Source inference ──────────────────────────────────────────────

  describe('source inference from entity ID', () => {
    const cases: Array<[string, EntitySource]> = [
      ['ICAO-A1B2C3', EntitySource.ADS_B],
      ['MMSI-123456789', EntitySource.AIS],
      ['JTN-42', EntitySource.LINK16],
      ['other-id', EntitySource.GPS],
    ];

    it.each(cases)(
      'infers %s from entity ID "%s"',
      async (entityId, expectedSource) => {
        const message = {
          entity_id: entityId,
          entity_type: 'unknown',
          name: 'Test',
          source: 'tcp',
          latitude: 10,
          longitude: 20,
          altitude: 0,
          heading: 0,
          speed_knots: 0,
          course: 0,
          timestamp: '2025-01-15T12:00:00Z',
        };

        await consumer.handleIngestMessage(message);

        expect(entityService.create).toHaveBeenCalledWith(
          expect.objectContaining({ source: expectedSource }),
        );
      },
    );
  });
});
