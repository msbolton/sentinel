import { Test, TestingModule } from '@nestjs/testing';
import { getRepositoryToken } from '@nestjs/typeorm';
import { TrackBatchService } from './track-batch.service';
import { TrackPoint } from './track-point.entity';

describe('TrackBatchService', () => {
  let service: TrackBatchService;
  let mockRepo: { query: jest.Mock; createQueryBuilder: jest.Mock };

  beforeEach(async () => {
    jest.useFakeTimers();

    const insertBuilderMock = {
      insert: jest.fn().mockReturnThis(),
      into: jest.fn().mockReturnThis(),
      values: jest.fn().mockReturnThis(),
      execute: jest.fn().mockResolvedValue(undefined),
    };

    mockRepo = {
      query: jest.fn().mockResolvedValue(undefined),
      createQueryBuilder: jest.fn().mockReturnValue(insertBuilderMock),
    };

    const module: TestingModule = await Test.createTestingModule({
      providers: [
        TrackBatchService,
        {
          provide: getRepositoryToken(TrackPoint),
          useValue: mockRepo,
        },
      ],
    }).compile();

    service = module.get<TrackBatchService>(TrackBatchService);

    // Clear the interval started in the constructor to prevent
    // interference with individual test expectations.
    jest.clearAllTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  function makePoint(overrides: Partial<{
    entityId: string;
    latitude: number;
    longitude: number;
    heading: number | null;
    speedKnots: number | null;
    course: number | null;
    source: string | null;
    timestamp: Date;
  }> = {}) {
    return {
      entityId: overrides.entityId ?? '550e8400-e29b-41d4-a716-446655440000',
      latitude: overrides.latitude ?? 10.0,
      longitude: overrides.longitude ?? 20.0,
      heading: overrides.heading ?? null,
      speedKnots: overrides.speedKnots ?? null,
      course: overrides.course ?? null,
      source: overrides.source ?? 'AIS',
      timestamp: overrides.timestamp ?? new Date('2025-01-01T00:00:00Z'),
    };
  }

  describe('addPoint', () => {
    it('should add a point to the buffer', async () => {
      expect(service.bufferSize).toBe(0);

      await service.addPoint(makePoint());

      expect(service.bufferSize).toBe(1);
    });

    it('should trigger flush when buffer reaches BATCH_SIZE (100)', async () => {
      // Add 99 points without triggering a flush
      for (let i = 0; i < 99; i++) {
        await service.addPoint(makePoint({ latitude: i }));
      }

      expect(mockRepo.query).not.toHaveBeenCalled();
      expect(service.bufferSize).toBe(99);

      // The 100th point should trigger flush
      await service.addPoint(makePoint({ latitude: 99 }));

      expect(mockRepo.query).toHaveBeenCalledTimes(1);
      expect(service.bufferSize).toBe(0);
    });

    it('should drop points when buffer is at MAX_BUFFER_SIZE (10000)', async () => {
      // Directly fill the internal buffer to MAX_BUFFER_SIZE via reflection
      // to avoid the memory cost of 10k individual addPoint calls.
      const internalBuffer = (service as any).buffer as unknown[];
      for (let i = 0; i < 10_000; i++) {
        internalBuffer.push(makePoint({ latitude: i }));
      }

      expect(service.bufferSize).toBe(10_000);

      // Now adding one more should be dropped
      await service.addPoint(makePoint({ latitude: 99999 }));

      // Buffer should still be 10_000 (point was dropped)
      expect(service.bufferSize).toBe(10_000);
    });
  });

  describe('flush', () => {
    it('should execute bulk INSERT SQL via trackPointRepo.query()', async () => {
      await service.addPoint(makePoint({ entityId: 'aaa-bbb', latitude: 1.0, longitude: 2.0 }));
      await service.addPoint(makePoint({ entityId: 'ccc-ddd', latitude: 3.0, longitude: 4.0 }));

      await service.flush();

      expect(mockRepo.query).toHaveBeenCalledTimes(1);

      const [sql, params] = mockRepo.query.mock.calls[0];
      expect(sql).toContain('INSERT INTO sentinel.track_points');
      expect(sql).toContain('ST_SetSRID(ST_MakePoint');
      // Verify params contain our entity IDs
      expect(params).toContain('aaa-bbb');
      expect(params).toContain('ccc-ddd');
    });

    it('should re-queue failed points on error', async () => {
      await service.addPoint(makePoint());
      await service.addPoint(makePoint());

      expect(service.bufferSize).toBe(2);

      mockRepo.query.mockRejectedValueOnce(new Error('DB connection failed'));

      await service.flush();

      // Points should be re-queued back into the buffer
      expect(service.bufferSize).toBe(2);
    });

    it('should be a no-op when buffer is empty', async () => {
      expect(service.bufferSize).toBe(0);

      await service.flush();

      expect(mockRepo.query).not.toHaveBeenCalled();
      expect(service.bufferSize).toBe(0);
    });
  });
});
