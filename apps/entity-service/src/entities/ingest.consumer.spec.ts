import { Test, TestingModule } from '@nestjs/testing';
import { IngestConsumer } from './ingest.consumer';
import { EntityService } from './entity.service';
import { EntityRepository } from './entity.repository';
import { EntityType, EntitySource, Classification } from './enums';
import { DataSource } from 'typeorm';

describe('IngestConsumer', () => {
  let consumer: IngestConsumer;
  let entityService: {
    create: jest.Mock;
    update: jest.Mock;
    updatePosition: jest.Mock;
    emitPositionEvent: jest.Mock;
    updateRedisGeo: jest.Mock;
  };
  let entityRepository: {
    createQueryBuilder: jest.Mock;
    findBySourceEntityIds: jest.Mock;
    bulkUpdatePositions: jest.Mock;
  };
  let dataSource: {
    query: jest.Mock;
  };

  beforeEach(async () => {
    entityService = {
      create: jest.fn().mockResolvedValue({ id: 'new-uuid' }),
      update: jest.fn().mockResolvedValue({}),
      updatePosition: jest.fn().mockResolvedValue({}),
      emitPositionEvent: jest.fn(),
      updateRedisGeo: jest.fn().mockResolvedValue(undefined),
    };

    entityRepository = {
      createQueryBuilder: jest.fn(),
      findBySourceEntityIds: jest.fn().mockResolvedValue(new Map()),
      bulkUpdatePositions: jest.fn().mockResolvedValue(undefined),
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

  afterEach(async () => {
    await consumer.onModuleDestroy();
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

  // ─── Batch processing ────────────────────────────────────────────

  describe('batch processing', () => {
    it('should partition batch into new, position updates, and name updates', async () => {
      const existingMap = new Map([
        ['MMSI-111', { id: 'uuid-1', name: 'Old Name 1', entityType: 'VESSEL', classification: 'UNCLASSIFIED', source: 'AIS', metadata: { sourceEntityId: 'MMSI-111' } }],
        ['MMSI-222', { id: 'uuid-2', name: 'Old Name 2', entityType: 'VESSEL', classification: 'UNCLASSIFIED', source: 'AIS', metadata: { sourceEntityId: 'MMSI-222' } }],
        ['MMSI-333', { id: 'uuid-3', name: 'Old Name 3', entityType: 'VESSEL', classification: 'UNCLASSIFIED', source: 'AIS', metadata: { sourceEntityId: 'MMSI-333' } }],
      ]);
      entityRepository.findBySourceEntityIds.mockResolvedValue(existingMap);

      const messages = [
        // Existing with position update
        {
          entity_id: 'MMSI-111',
          entity_type: 'vessel',
          name: 'Old Name 1',
          source: 'ais',
          latitude: 38.9,
          longitude: -77.0,
          altitude: 0,
          heading: 180,
          speed_knots: 12,
          course: 180,
          timestamp: '2025-01-15T12:00:00Z',
        },
        // Existing with name change (no position)
        {
          entity_id: 'MMSI-222',
          entity_type: 'vessel',
          name: 'EVER GIVEN',
          source: 'ais',
          latitude: 0,
          longitude: 0,
          altitude: 0,
          heading: 0,
          speed_knots: 0,
          course: 0,
          timestamp: '2025-01-15T12:00:00Z',
        },
        // Existing with position + name change
        {
          entity_id: 'MMSI-333',
          entity_type: 'vessel',
          name: 'NEW NAME',
          source: 'ais',
          latitude: 40.0,
          longitude: -74.0,
          altitude: 0,
          heading: 90,
          speed_knots: 8,
          course: 90,
          timestamp: '2025-01-15T12:00:00Z',
        },
        // New entity
        {
          entity_id: 'ICAO-A1B2C3',
          entity_type: 'aircraft',
          name: 'BAW123',
          source: 'adsb',
          latitude: 51.5,
          longitude: -0.1,
          altitude: 10000,
          heading: 90,
          speed_knots: 450,
          course: 90,
          timestamp: '2025-01-15T12:00:00Z',
        },
      ];

      for (const msg of messages) {
        await consumer.handleIngestMessage(msg);
      }
      await consumer.flushBuffer();

      // Single bulk lookup
      expect(entityRepository.findBySourceEntityIds).toHaveBeenCalledWith([
        'MMSI-111',
        'MMSI-222',
        'MMSI-333',
        'ICAO-A1B2C3',
      ]);

      // Bulk position update for 2 entities (MMSI-111 and MMSI-333)
      expect(entityRepository.bulkUpdatePositions).toHaveBeenCalledWith(
        expect.arrayContaining([
          expect.objectContaining({ id: 'uuid-1', lat: 38.9, lng: -77.0 }),
          expect.objectContaining({ id: 'uuid-3', lat: 40.0, lng: -74.0 }),
        ]),
      );

      // Position events emitted for each update
      expect(entityService.emitPositionEvent).toHaveBeenCalledTimes(2);
      expect(entityService.updateRedisGeo).toHaveBeenCalledTimes(2);

      // 1 new entity created
      expect(entityService.create).toHaveBeenCalledTimes(1);
      expect(entityService.create).toHaveBeenCalledWith(
        expect.objectContaining({
          entityType: EntityType.AIRCRAFT,
          source: EntitySource.ADS_B,
          name: 'BAW123',
        }),
      );

      // 2 name updates (MMSI-222 and MMSI-333)
      expect(entityService.update).toHaveBeenCalledTimes(2);
      expect(entityService.update).toHaveBeenCalledWith('uuid-2', {
        name: 'EVER GIVEN',
      });
      expect(entityService.update).toHaveBeenCalledWith('uuid-3', {
        name: 'NEW NAME',
      });
    });

    it('should be a no-op when buffer is empty', async () => {
      await consumer.flushBuffer();

      expect(entityRepository.findBySourceEntityIds).not.toHaveBeenCalled();
      expect(entityRepository.bulkUpdatePositions).not.toHaveBeenCalled();
      expect(entityService.create).not.toHaveBeenCalled();
    });

    it('should skip position update for zero lat/lon messages', async () => {
      const existingMap = new Map([
        ['MMSI-111', { id: 'uuid-1', name: 'Test', entityType: 'VESSEL', classification: 'UNCLASSIFIED', source: 'AIS', metadata: { sourceEntityId: 'MMSI-111' } }],
      ]);
      entityRepository.findBySourceEntityIds.mockResolvedValue(existingMap);

      await consumer.handleIngestMessage({
        entity_id: 'MMSI-111',
        entity_type: 'vessel',
        name: 'Test',
        source: 'ais',
        latitude: 0,
        longitude: 0,
        altitude: 0,
        heading: 0,
        speed_knots: 0,
        course: 0,
        timestamp: '2025-01-15T12:00:00Z',
      });
      await consumer.flushBuffer();

      expect(entityRepository.bulkUpdatePositions).not.toHaveBeenCalled();
    });

    it('should continue processing when one entity creation fails', async () => {
      entityService.create
        .mockRejectedValueOnce(new Error('DB error'))
        .mockResolvedValueOnce({ id: 'uuid-2' });

      await consumer.handleIngestMessage({
        entity_id: 'FAIL-1',
        entity_type: 'unknown',
        name: 'Fail',
        source: '',
        latitude: 10,
        longitude: 20,
        altitude: 0,
        heading: 0,
        speed_knots: 0,
        course: 0,
        timestamp: '2025-01-15T12:00:00Z',
      });
      await consumer.handleIngestMessage({
        entity_id: 'OK-1',
        entity_type: 'unknown',
        name: 'OK',
        source: '',
        latitude: 10,
        longitude: 20,
        altitude: 0,
        heading: 0,
        speed_knots: 0,
        course: 0,
        timestamp: '2025-01-15T12:00:00Z',
      });
      await consumer.flushBuffer();

      expect(entityService.create).toHaveBeenCalledTimes(2);
    });
  });

  // ─── New entity creation (via batch) ──────────────────────────────

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
      await consumer.flushBuffer();

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
    it('should bulk update position', async () => {
      const existingMap = new Map([
        ['MMSI-123456789', { id: 'existing-uuid', name: 'MMSI 123456789', entityType: 'VESSEL', classification: 'UNCLASSIFIED', source: 'AIS', metadata: { sourceEntityId: 'MMSI-123456789' } }],
      ]);
      entityRepository.findBySourceEntityIds.mockResolvedValue(existingMap);

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
      await consumer.flushBuffer();

      expect(entityRepository.bulkUpdatePositions).toHaveBeenCalledWith([
        expect.objectContaining({
          id: 'existing-uuid',
          lat: 38.9,
          lng: -77.0,
          heading: 180,
          speedKnots: 12,
          course: 180,
        }),
      ]);
      expect(entityService.create).not.toHaveBeenCalled();
    });
  });

  // ─── Existing entity with name change ──────────────────────────────

  describe('existing entity with name change', () => {
    it('should call update with new name', async () => {
      const existingMap = new Map([
        ['MMSI-123456789', { id: 'existing-uuid', name: 'MMSI 123456789', entityType: 'VESSEL', classification: 'UNCLASSIFIED', source: 'AIS', metadata: { sourceEntityId: 'MMSI-123456789' } }],
      ]);
      entityRepository.findBySourceEntityIds.mockResolvedValue(existingMap);

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
      await consumer.flushBuffer();

      expect(entityService.update).toHaveBeenCalledWith('existing-uuid', {
        name: 'EVER GIVEN',
      });
    });
  });

  // ─── AIS Type 5 (zero lat/lon) ────────────────────────────────────

  describe('AIS Type 5 with zero lat/lon', () => {
    it('should skip position update but still update name', async () => {
      const existingMap = new Map([
        ['MMSI-123456789', { id: 'existing-uuid', name: 'MMSI 123456789', entityType: 'VESSEL', classification: 'UNCLASSIFIED', source: 'AIS', metadata: { sourceEntityId: 'MMSI-123456789' } }],
      ]);
      entityRepository.findBySourceEntityIds.mockResolvedValue(existingMap);

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
      await consumer.flushBuffer();

      expect(entityRepository.bulkUpdatePositions).not.toHaveBeenCalled();
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
        await consumer.flushBuffer();

        expect(entityService.create).toHaveBeenCalledWith(
          expect.objectContaining({ entityType: expectedType }),
        );
      },
    );
  });

  // ─── Source inference from entity ID prefix ──────────────────────

  describe('source inference from entity ID prefix', () => {
    const cases: Array<[string, EntitySource]> = [
      ['ICAO-A1B2C3', EntitySource.ADS_B],
      ['MMSI-123456789', EntitySource.AIS],
      ['JTN-42', EntitySource.LINK16],
      ['SAT-25544', EntitySource.CELESTRAK],
      ['other-id', EntitySource.GPS],
    ];

    it.each(cases)(
      'infers %s from entity ID "%s"',
      async (entityId, expectedSource) => {
        const message = {
          entity_id: entityId,
          entity_type: 'unknown',
          name: 'Test',
          source: '',
          latitude: 10,
          longitude: 20,
          altitude: 0,
          heading: 0,
          speed_knots: 0,
          course: 0,
          timestamp: '2025-01-15T12:00:00Z',
        };

        await consumer.handleIngestMessage(message);
        await consumer.flushBuffer();

        expect(entityService.create).toHaveBeenCalledWith(
          expect.objectContaining({ source: expectedSource }),
        );
      },
    );
  });

  // ─── Explicit source from ingest adapter ──────────────────────────

  describe('explicit source from message.source field', () => {
    const cases: Array<[string, EntitySource]> = [
      ['opensky', EntitySource.OPENSKY],
      ['adsblol', EntitySource.ADSB_LOL],
      ['celestrak', EntitySource.CELESTRAK],
      ['ais', EntitySource.AIS],
      ['adsb', EntitySource.ADS_B],
    ];

    it.each(cases)(
      'maps source "%s" to %s',
      async (sourceValue, expectedSource) => {
        const message = {
          entity_id: 'TEST-1',
          entity_type: 'unknown',
          name: 'Test',
          source: sourceValue,
          latitude: 10,
          longitude: 20,
          altitude: 0,
          heading: 0,
          speed_knots: 0,
          course: 0,
          timestamp: '2025-01-15T12:00:00Z',
        };

        await consumer.handleIngestMessage(message);
        await consumer.flushBuffer();

        expect(entityService.create).toHaveBeenCalledWith(
          expect.objectContaining({ source: expectedSource }),
        );
      },
    );

    it('prefers explicit source over entity ID prefix', async () => {
      const message = {
        entity_id: 'ICAO-A1B2C3',
        entity_type: 'aircraft',
        name: 'Test',
        source: 'opensky',
        latitude: 10,
        longitude: 20,
        altitude: 0,
        heading: 0,
        speed_knots: 0,
        course: 0,
        timestamp: '2025-01-15T12:00:00Z',
      };

      await consumer.handleIngestMessage(message);
      await consumer.flushBuffer();

      expect(entityService.create).toHaveBeenCalledWith(
        expect.objectContaining({ source: EntitySource.OPENSKY }),
      );
    });
  });
});
