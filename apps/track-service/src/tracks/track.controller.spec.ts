import { Test, TestingModule } from '@nestjs/testing';
import { TrackController } from './track.controller';
import { TrackService } from './track.service';
import { QueryTrackDto } from './dto/query-track.dto';

describe('TrackController', () => {
  let controller: TrackController;
  let trackService: jest.Mocked<TrackService>;

  beforeEach(async () => {
    const mockTrackService = {
      getHistory: jest.fn(),
      getLatestPositions: jest.fn(),
      getSegments: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      controllers: [TrackController],
      providers: [
        { provide: TrackService, useValue: mockTrackService },
      ],
    }).compile();

    controller = module.get<TrackController>(TrackController);
    trackService = module.get(TrackService);
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  describe('getTrackHistory', () => {
    it('should delegate to trackService.getHistory with parsed dates', async () => {
      const entityId = '550e8400-e29b-41d4-a716-446655440000';
      const query: QueryTrackDto = {
        startTime: '2025-01-01T00:00:00Z',
        endTime: '2025-01-02T00:00:00Z',
        maxPoints: 500,
        simplify: 0.5,
      };

      const expectedResult = [
        {
          id: 'point-1',
          entityId,
          latitude: 10.0,
          longitude: 20.0,
          heading: 90,
          speedKnots: 12,
          course: 180,
          altitude: null,
          source: 'AIS',
          timestamp: new Date('2025-01-01T01:00:00Z'),
        },
      ];

      trackService.getHistory.mockResolvedValue(expectedResult);

      const result = await controller.getTrackHistory(entityId, query);

      expect(trackService.getHistory).toHaveBeenCalledWith(
        entityId,
        new Date('2025-01-01T00:00:00Z'),
        new Date('2025-01-02T00:00:00Z'),
        500,
        0.5,
      );
      expect(result).toEqual(expectedResult);
    });

    it('should pass undefined dates when not provided in query', async () => {
      const entityId = '550e8400-e29b-41d4-a716-446655440000';
      const query: QueryTrackDto = {};

      trackService.getHistory.mockResolvedValue([]);

      await controller.getTrackHistory(entityId, query);

      expect(trackService.getHistory).toHaveBeenCalledWith(
        entityId,
        undefined,
        undefined,
        undefined,
        undefined,
      );
    });
  });

  describe('getLatestPosition', () => {
    it('should return the first result from getLatestPositions', async () => {
      const entityId = '550e8400-e29b-41d4-a716-446655440000';
      const latestPoint = {
        id: 'point-1',
        entityId,
        latitude: 10.0,
        longitude: 20.0,
        heading: 90,
        speedKnots: 12,
        course: 180,
        altitude: null,
        source: 'AIS',
        timestamp: new Date('2025-01-01T12:00:00Z'),
      };

      trackService.getLatestPositions.mockResolvedValue([latestPoint]);

      const result = await controller.getLatestPosition(entityId);

      expect(trackService.getLatestPositions).toHaveBeenCalledWith([entityId]);
      expect(result).toEqual(latestPoint);
    });

    it('should return null when no results are found', async () => {
      const entityId = '550e8400-e29b-41d4-a716-446655440000';

      trackService.getLatestPositions.mockResolvedValue([]);

      const result = await controller.getLatestPosition(entityId);

      expect(trackService.getLatestPositions).toHaveBeenCalledWith([entityId]);
      expect(result).toBeNull();
    });
  });

  describe('getTrackSegments', () => {
    it('should delegate to trackService.getSegments with parsed dates', async () => {
      const entityId = '550e8400-e29b-41d4-a716-446655440000';
      const query: QueryTrackDto = {
        startTime: '2025-01-01T00:00:00Z',
        endTime: '2025-01-02T00:00:00Z',
      };

      const expectedSegments = [
        {
          startTime: new Date('2025-01-01T01:00:00Z'),
          endTime: new Date('2025-01-01T02:00:00Z'),
          points: [],
        },
      ];

      trackService.getSegments.mockResolvedValue(expectedSegments);

      const result = await controller.getTrackSegments(entityId, query);

      expect(trackService.getSegments).toHaveBeenCalledWith(
        entityId,
        new Date('2025-01-01T00:00:00Z'),
        new Date('2025-01-02T00:00:00Z'),
      );
      expect(result).toEqual(expectedSegments);
    });

    it('should pass undefined dates when not provided in query', async () => {
      const entityId = '550e8400-e29b-41d4-a716-446655440000';
      const query: QueryTrackDto = {};

      trackService.getSegments.mockResolvedValue([]);

      await controller.getTrackSegments(entityId, query);

      expect(trackService.getSegments).toHaveBeenCalledWith(
        entityId,
        undefined,
        undefined,
      );
    });
  });
});
