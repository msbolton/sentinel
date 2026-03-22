import { Test, TestingModule } from '@nestjs/testing';
import { AgeoutService } from './ageout.service';
import { DataSource } from 'typeorm';
import { getRepositoryToken } from '@nestjs/typeorm';
import { AgeoutConfigRecord } from './ageout-config.entity';

describe('AgeoutService', () => {
  let service: AgeoutService;
  let dataSource: { query: jest.Mock };
  let configRepo: { find: jest.Mock; count: jest.Mock; save: jest.Mock };
  let kafkaClient: { emit: jest.Mock };

  beforeEach(async () => {
    dataSource = { query: jest.fn() };
    configRepo = { find: jest.fn(), count: jest.fn().mockResolvedValue(1), save: jest.fn() };
    kafkaClient = { emit: jest.fn() };

    const module: TestingModule = await Test.createTestingModule({
      providers: [
        AgeoutService,
        { provide: DataSource, useValue: dataSource },
        { provide: getRepositoryToken(AgeoutConfigRecord), useValue: configRepo },
        { provide: 'KAFKA_CLIENT', useValue: kafkaClient },
      ],
    }).compile();

    service = module.get<AgeoutService>(AgeoutService);
  });

  it('should be defined', () => {
    expect(service).toBeDefined();
  });

  describe('processAgeout', () => {
    it('should transition LIVE entities to STALE when past stale threshold', async () => {
      const staleResults = [
        { id: 'e1', entityType: 'AIRCRAFT', source: 'ADS_B', feedId: 'f1', lastSeenAt: new Date(), threshold_ms: 60000 },
      ];
      // First query: LIVE→STALE, second query: STALE→AGED_OUT
      dataSource.query.mockResolvedValueOnce(staleResults);
      dataSource.query.mockResolvedValueOnce([]);

      await service.processAgeout();

      expect(dataSource.query).toHaveBeenCalledTimes(2);
      expect(kafkaClient.emit).toHaveBeenCalledWith(
        'events.entity.stale',
        expect.objectContaining({
          key: 'e1',
        }),
      );
    });

    it('should transition STALE entities to AGED_OUT when past ageout threshold', async () => {
      const agedOutResults = [
        { id: 'e2', entityType: 'VESSEL', source: 'AIS', feedId: 'f2', lastSeenAt: new Date(), threshold_ms: 1800000 },
      ];
      dataSource.query.mockResolvedValueOnce([]);
      dataSource.query.mockResolvedValueOnce(agedOutResults);

      await service.processAgeout();

      expect(kafkaClient.emit).toHaveBeenCalledWith(
        'events.entity.agedout',
        expect.objectContaining({
          key: 'e2',
        }),
      );
    });

    it('should not process if already running', async () => {
      dataSource.query.mockImplementation(() => new Promise(resolve => setTimeout(() => resolve([]), 100)));

      const promise1 = service.processAgeout();
      const promise2 = service.processAgeout();

      await Promise.all([promise1, promise2]);

      // Only one cycle should have run the queries
      expect(dataSource.query).toHaveBeenCalledTimes(2); // 2 queries per cycle, but only 1 cycle
    });

    it('should handle empty results gracefully', async () => {
      dataSource.query.mockResolvedValue([]);

      await service.processAgeout();

      expect(dataSource.query).toHaveBeenCalledTimes(2);
      expect(kafkaClient.emit).not.toHaveBeenCalled();
    });
  });
});
