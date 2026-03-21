import { Test } from '@nestjs/testing';
import { HttpService } from '@nestjs/axios';
import { ExecutionContext } from '@nestjs/common';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { TracksController } from './tracks.controller';
import { TracksService } from './tracks.service';

describe('TracksController', () => {
  let controller: TracksController;
  let service: jest.Mocked<TracksService>;

  beforeEach(async () => {
    const mockService = {
      getHistory: jest.fn(),
      getLatestPosition: jest.fn(),
      getSegments: jest.fn(),
      getReplayStreamUrl: jest.fn(),
    };

    const module = await Test.createTestingModule({
      controllers: [TracksController],
      providers: [
        { provide: TracksService, useValue: mockService },
        { provide: HttpService, useValue: {} },
      ],
    })
      .overrideGuard(JwtAuthGuard)
      .useValue({ canActivate: (_ctx: ExecutionContext) => true })
      .compile();

    controller = module.get(TracksController);
    service = module.get(TracksService);
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  describe('getHistory', () => {
    it('should delegate to service', async () => {
      const points = [{ id: '1', latitude: 10, longitude: 20 }];
      service.getHistory.mockResolvedValue(points);

      const result = await controller.getHistory('550e8400-e29b-41d4-a716-446655440000', {});
      expect(service.getHistory).toHaveBeenCalledWith('550e8400-e29b-41d4-a716-446655440000', {});
      expect(result).toEqual(points);
    });
  });

  describe('getLatestPosition', () => {
    it('should delegate to service', async () => {
      const point = { id: '1', latitude: 10, longitude: 20 };
      service.getLatestPosition.mockResolvedValue(point);

      const result = await controller.getLatestPosition('550e8400-e29b-41d4-a716-446655440000');
      expect(service.getLatestPosition).toHaveBeenCalledWith('550e8400-e29b-41d4-a716-446655440000');
      expect(result).toEqual(point);
    });
  });

  describe('getSegments', () => {
    it('should delegate to service', async () => {
      const segments = [{ startTime: '2025-01-01', endTime: '2025-01-02', points: [] }];
      service.getSegments.mockResolvedValue(segments);

      const result = await controller.getSegments('550e8400-e29b-41d4-a716-446655440000', {});
      expect(service.getSegments).toHaveBeenCalledWith('550e8400-e29b-41d4-a716-446655440000', {});
      expect(result).toEqual(segments);
    });
  });
});
