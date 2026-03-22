import { Test, TestingModule } from '@nestjs/testing';
import { AgeoutConfigController } from './ageout-config.controller';
import { getRepositoryToken } from '@nestjs/typeorm';
import { AgeoutConfigRecord } from './ageout-config.entity';

describe('AgeoutConfigController', () => {
  let controller: AgeoutConfigController;
  let repo: {
    find: jest.Mock;
    findOne: jest.Mock;
    save: jest.Mock;
    delete: jest.Mock;
  };

  beforeEach(async () => {
    repo = {
      find: jest.fn(),
      findOne: jest.fn(),
      save: jest.fn(),
      delete: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      controllers: [AgeoutConfigController],
      providers: [
        { provide: getRepositoryToken(AgeoutConfigRecord), useValue: repo },
      ],
    }).compile();

    controller = module.get<AgeoutConfigController>(AgeoutConfigController);
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  describe('findAll', () => {
    it('should return all ageout configs', async () => {
      const configs = [
        { id: 'uuid-1', sourceType: 'ADS_B', staleThresholdMs: 60000, ageoutThresholdMs: 300000 },
      ];
      repo.find.mockResolvedValue(configs);

      const result = await controller.findAll();
      expect(result).toEqual(configs);
      expect(repo.find).toHaveBeenCalled();
    });
  });

  describe('findBySourceType', () => {
    it('should return config for source type with feedId null', async () => {
      const config = { id: 'uuid-1', sourceType: 'ADS_B', feedId: null, staleThresholdMs: 60000, ageoutThresholdMs: 300000 };
      repo.findOne.mockResolvedValue(config);

      const result = await controller.findBySourceType('ADS_B', undefined);
      expect(result).toEqual(config);
      expect(repo.findOne).toHaveBeenCalledWith({
        where: { sourceType: 'ADS_B', feedId: null },
      });
    });
  });

  describe('upsert', () => {
    it('should create new config when none exists', async () => {
      repo.findOne.mockResolvedValue(null);
      const dto = { sourceType: 'AIS', staleThresholdMs: 600000, ageoutThresholdMs: 1800000 };
      const saved = { id: 'uuid-2', feedId: null, ...dto };
      repo.save.mockResolvedValue(saved);

      const result = await controller.upsert(dto as any);
      expect(result).toEqual(saved);
    });

    it('should update existing config', async () => {
      const existing = { id: 'uuid-1', sourceType: 'ADS_B', feedId: null, staleThresholdMs: 60000, ageoutThresholdMs: 300000 };
      repo.findOne.mockResolvedValue(existing);
      const dto = { sourceType: 'ADS_B', staleThresholdMs: 30000, ageoutThresholdMs: 120000 };
      const updated = { ...existing, ...dto };
      repo.save.mockResolvedValue(updated);

      const result = await controller.upsert(dto as any);
      expect(result.staleThresholdMs).toBe(30000);
    });
  });

  describe('remove', () => {
    it('should delete config by id', async () => {
      repo.delete.mockResolvedValue({ affected: 1 });

      await controller.remove('uuid-1');
      expect(repo.delete).toHaveBeenCalledWith('uuid-1');
    });
  });
});
