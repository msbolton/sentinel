import { Test, TestingModule } from '@nestjs/testing';
import { getRepositoryToken } from '@nestjs/typeorm';
import { NotFoundException } from '@nestjs/common';
import { AlertService } from './alert.service';
import { AlertRecord } from './alert.entity';
import { AlertRuleRecord } from './alert-rule.entity';
import {
  AlertType,
  AlertSeverity,
  RuleType,
} from './alert-type.enum';

// ── Helper: build a chainable query-builder mock ──────────────────
function createQueryBuilderMock(returnData: {
  getMany?: unknown[];
  getOne?: unknown;
  getCount?: number;
} = {}) {
  const qb: Record<string, jest.Mock> = {};

  // Every chainable method returns the same qb instance
  const chain = () => qb;
  qb.where = jest.fn().mockImplementation(chain);
  qb.andWhere = jest.fn().mockImplementation(chain);
  qb.orderBy = jest.fn().mockImplementation(chain);
  qb.skip = jest.fn().mockImplementation(chain);
  qb.take = jest.fn().mockImplementation(chain);

  // Terminal methods
  qb.getMany = jest.fn().mockResolvedValue(returnData.getMany ?? []);
  qb.getOne = jest.fn().mockResolvedValue(returnData.getOne ?? null);
  qb.getCount = jest.fn().mockResolvedValue(returnData.getCount ?? 0);

  return qb;
}

describe('AlertService', () => {
  let service: AlertService;
  let alertRepo: Record<string, jest.Mock>;
  let ruleRepo: Record<string, jest.Mock>;
  let kafkaClient: Record<string, jest.Mock>;

  beforeEach(async () => {
    alertRepo = {
      create: jest.fn(),
      save: jest.fn(),
      findOne: jest.fn(),
      query: jest.fn(),
      createQueryBuilder: jest.fn(),
    };

    ruleRepo = {
      create: jest.fn(),
      save: jest.fn(),
      find: jest.fn(),
      findOne: jest.fn(),
      createQueryBuilder: jest.fn(),
    };

    kafkaClient = {
      emit: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      providers: [
        AlertService,
        {
          provide: getRepositoryToken(AlertRecord),
          useValue: alertRepo,
        },
        {
          provide: getRepositoryToken(AlertRuleRecord),
          useValue: ruleRepo,
        },
        {
          provide: 'KAFKA_SERVICE',
          useValue: kafkaClient,
        },
      ],
    }).compile();

    service = module.get<AlertService>(AlertService);
  });

  it('should be defined', () => {
    expect(service).toBeDefined();
  });

  // ── createRule ──────────────────────────────────────────────────

  describe('createRule', () => {
    it('should create and save a new rule', async () => {
      const dto = {
        name: 'Speed Limit',
        ruleType: RuleType.SPEED_THRESHOLD,
        config: { maxSpeedKnots: 50 },
        monitoredEntityTypes: ['vessel'],
        severity: AlertSeverity.HIGH,
        enabled: true,
      };

      const created = { ...dto, id: 'rule-1' };
      ruleRepo.create.mockReturnValue(created);
      ruleRepo.save.mockResolvedValue({ ...created, createdAt: new Date() });

      const result = await service.createRule(dto as any);

      expect(ruleRepo.create).toHaveBeenCalledWith({
        name: dto.name,
        ruleType: dto.ruleType,
        config: dto.config,
        monitoredEntityTypes: dto.monitoredEntityTypes,
        severity: dto.severity,
        enabled: dto.enabled,
      });
      expect(ruleRepo.save).toHaveBeenCalledWith(created);
      expect(result).toHaveProperty('createdAt');
    });

    it('should default severity to MEDIUM and enabled to true when not specified', async () => {
      const dto = {
        name: 'Geofence Rule',
        ruleType: RuleType.GEOFENCE,
        config: { polygon: [] },
      };

      const created = { ...dto, id: 'rule-2' };
      ruleRepo.create.mockReturnValue(created);
      ruleRepo.save.mockResolvedValue(created);

      await service.createRule(dto as any);

      expect(ruleRepo.create).toHaveBeenCalledWith(
        expect.objectContaining({
          severity: AlertSeverity.MEDIUM,
          enabled: true,
          monitoredEntityTypes: [],
        }),
      );
    });
  });

  // ── getRules ────────────────────────────────────────────────────

  describe('getRules', () => {
    it('should return rules ordered by createdAt DESC', async () => {
      const rules = [
        { id: 'rule-2', name: 'B', createdAt: new Date('2025-02-01') },
        { id: 'rule-1', name: 'A', createdAt: new Date('2025-01-01') },
      ];

      ruleRepo.find.mockResolvedValue(rules);

      const result = await service.getRules();

      expect(ruleRepo.find).toHaveBeenCalledWith({
        order: { createdAt: 'DESC' },
      });
      expect(result).toEqual(rules);
    });
  });

  // ── updateRule ──────────────────────────────────────────────────

  describe('updateRule', () => {
    it('should update an existing rule', async () => {
      const existing = {
        id: 'rule-1',
        name: 'Old Name',
        enabled: true,
      };

      const dto = { name: 'New Name', enabled: false };

      ruleRepo.findOne.mockResolvedValue({ ...existing });
      ruleRepo.save.mockImplementation(async (entity) => entity);

      const result = await service.updateRule('rule-1', dto as any);

      expect(ruleRepo.findOne).toHaveBeenCalledWith({
        where: { id: 'rule-1' },
      });
      expect(result.name).toBe('New Name');
      expect(result.enabled).toBe(false);
      expect(ruleRepo.save).toHaveBeenCalled();
    });

    it('should throw NotFoundException when rule does not exist', async () => {
      ruleRepo.findOne.mockResolvedValue(null);

      await expect(
        service.updateRule('nonexistent-id', { name: 'X' } as any),
      ).rejects.toThrow(NotFoundException);
    });
  });

  // ── evaluateSpeedAnomaly ────────────────────────────────────────

  describe('evaluateSpeedAnomaly', () => {
    it('should create an alert when speed exceeds maxSpeedKnots', async () => {
      const rule: Partial<AlertRuleRecord> = {
        id: 'rule-speed-1',
        name: 'Max Speed',
        ruleType: RuleType.SPEED_THRESHOLD,
        severity: AlertSeverity.HIGH,
        enabled: true,
        config: { maxSpeedKnots: 50 },
        monitoredEntityTypes: ['vessel'],
      };

      const ruleQb = createQueryBuilderMock({ getMany: [rule] });
      ruleRepo.createQueryBuilder.mockReturnValue(ruleQb);

      const savedAlert = {
        id: 'alert-1',
        alertType: AlertType.SPEED_ANOMALY,
        severity: AlertSeverity.HIGH,
        entityId: 'entity-1',
        createdAt: new Date(),
      };

      alertRepo.create.mockReturnValue(savedAlert);
      alertRepo.save.mockResolvedValue(savedAlert);

      const alerts = await service.evaluateSpeedAnomaly(
        'entity-1',
        75,
        'vessel',
      );

      expect(alerts).toHaveLength(1);
      expect(alerts[0]).toEqual(savedAlert);
      expect(alertRepo.create).toHaveBeenCalledWith(
        expect.objectContaining({
          alertType: AlertType.SPEED_ANOMALY,
          severity: AlertSeverity.HIGH,
          entityId: 'entity-1',
        }),
      );
      expect(kafkaClient.emit).toHaveBeenCalledWith(
        'alerts.anomaly',
        expect.objectContaining({
          key: 'entity-1',
        }),
      );
    });

    it('should create an alert when speed is below minSpeedKnots', async () => {
      const rule: Partial<AlertRuleRecord> = {
        id: 'rule-speed-2',
        name: 'Min Speed',
        ruleType: RuleType.SPEED_THRESHOLD,
        severity: AlertSeverity.MEDIUM,
        enabled: true,
        config: { minSpeedKnots: 5 },
        monitoredEntityTypes: [],
      };

      const ruleQb = createQueryBuilderMock({ getMany: [rule] });
      ruleRepo.createQueryBuilder.mockReturnValue(ruleQb);

      const savedAlert = {
        id: 'alert-2',
        alertType: AlertType.SPEED_ANOMALY,
        severity: AlertSeverity.MEDIUM,
        entityId: 'entity-2',
        createdAt: new Date(),
      };

      alertRepo.create.mockReturnValue(savedAlert);
      alertRepo.save.mockResolvedValue(savedAlert);

      const alerts = await service.evaluateSpeedAnomaly(
        'entity-2',
        2,
        'vessel',
      );

      expect(alerts).toHaveLength(1);
      expect(alertRepo.create).toHaveBeenCalledWith(
        expect.objectContaining({
          alertType: AlertType.SPEED_ANOMALY,
          entityId: 'entity-2',
        }),
      );
    });

    it('should not create an alert when speed is within bounds', async () => {
      const rule: Partial<AlertRuleRecord> = {
        id: 'rule-speed-3',
        name: 'Normal Range',
        ruleType: RuleType.SPEED_THRESHOLD,
        severity: AlertSeverity.LOW,
        enabled: true,
        config: { maxSpeedKnots: 50, minSpeedKnots: 5 },
        monitoredEntityTypes: [],
      };

      const ruleQb = createQueryBuilderMock({ getMany: [rule] });
      ruleRepo.createQueryBuilder.mockReturnValue(ruleQb);

      const alerts = await service.evaluateSpeedAnomaly(
        'entity-3',
        25,
        'vessel',
      );

      expect(alerts).toHaveLength(0);
      expect(alertRepo.create).not.toHaveBeenCalled();
    });

    it('should filter rules by entity type', async () => {
      const vesselRule: Partial<AlertRuleRecord> = {
        id: 'rule-vessel',
        name: 'Vessel Only',
        ruleType: RuleType.SPEED_THRESHOLD,
        severity: AlertSeverity.HIGH,
        enabled: true,
        config: { maxSpeedKnots: 30 },
        monitoredEntityTypes: ['vessel'],
      };

      const aircraftRule: Partial<AlertRuleRecord> = {
        id: 'rule-aircraft',
        name: 'Aircraft Only',
        ruleType: RuleType.SPEED_THRESHOLD,
        severity: AlertSeverity.HIGH,
        enabled: true,
        config: { maxSpeedKnots: 10 },
        monitoredEntityTypes: ['aircraft'],
      };

      // getEnabledRules returns both from DB, then filters in-memory
      const ruleQb = createQueryBuilderMock({
        getMany: [vesselRule, aircraftRule],
      });
      ruleRepo.createQueryBuilder.mockReturnValue(ruleQb);

      // Speed of 35 exceeds vessel rule (30) but the aircraft rule (10) should be
      // filtered out by entity type, so only the vessel rule triggers
      const savedAlert = {
        id: 'alert-vessel',
        alertType: AlertType.SPEED_ANOMALY,
        severity: AlertSeverity.HIGH,
        entityId: 'entity-v1',
        createdAt: new Date(),
      };

      alertRepo.create.mockReturnValue(savedAlert);
      alertRepo.save.mockResolvedValue(savedAlert);

      const alerts = await service.evaluateSpeedAnomaly(
        'entity-v1',
        35,
        'vessel',
      );

      // Only the vessel rule should trigger (aircraft rule filtered out)
      expect(alerts).toHaveLength(1);
      expect(alertRepo.create).toHaveBeenCalledTimes(1);
    });
  });

  // ── getAlerts ───────────────────────────────────────────────────

  describe('getAlerts', () => {
    it('should return paginated alerts with default page/pageSize', async () => {
      const mockAlerts = [
        { id: 'a1', title: 'Alert 1' },
        { id: 'a2', title: 'Alert 2' },
      ];

      const qb = createQueryBuilderMock({
        getMany: mockAlerts,
        getCount: 2,
      });
      alertRepo.createQueryBuilder.mockReturnValue(qb);

      const result = await service.getAlerts({});

      expect(alertRepo.createQueryBuilder).toHaveBeenCalledWith('alert');
      expect(qb.orderBy).toHaveBeenCalledWith('alert.createdAt', 'DESC');
      expect(qb.skip).toHaveBeenCalledWith(0); // (1 - 1) * 20
      expect(qb.take).toHaveBeenCalledWith(20);
      expect(result).toEqual({
        data: mockAlerts,
        total: 2,
        page: 1,
        pageSize: 20,
      });
    });

    it('should apply severity filter', async () => {
      const qb = createQueryBuilderMock({ getMany: [], getCount: 0 });
      alertRepo.createQueryBuilder.mockReturnValue(qb);

      await service.getAlerts({ severity: AlertSeverity.CRITICAL });

      expect(qb.andWhere).toHaveBeenCalledWith(
        'alert.severity = :severity',
        { severity: AlertSeverity.CRITICAL },
      );
    });

    it('should apply types filter', async () => {
      const qb = createQueryBuilderMock({ getMany: [], getCount: 0 });
      alertRepo.createQueryBuilder.mockReturnValue(qb);

      await service.getAlerts({
        types: [AlertType.SPEED_ANOMALY, AlertType.GEOFENCE_ENTRY],
      });

      expect(qb.andWhere).toHaveBeenCalledWith(
        'alert."alertType" IN (:...types)',
        { types: [AlertType.SPEED_ANOMALY, AlertType.GEOFENCE_ENTRY] },
      );
    });

    it('should apply entityId filter', async () => {
      const qb = createQueryBuilderMock({ getMany: [], getCount: 0 });
      alertRepo.createQueryBuilder.mockReturnValue(qb);

      await service.getAlerts({ entityId: 'entity-abc' });

      expect(qb.andWhere).toHaveBeenCalledWith(
        'alert."entityId" = :entityId',
        { entityId: 'entity-abc' },
      );
    });

    it('should apply acknowledged=true filter', async () => {
      const qb = createQueryBuilderMock({ getMany: [], getCount: 0 });
      alertRepo.createQueryBuilder.mockReturnValue(qb);

      await service.getAlerts({ acknowledged: true });

      expect(qb.andWhere).toHaveBeenCalledWith(
        'alert."acknowledgedAt" IS NOT NULL',
      );
    });

    it('should apply acknowledged=false filter', async () => {
      const qb = createQueryBuilderMock({ getMany: [], getCount: 0 });
      alertRepo.createQueryBuilder.mockReturnValue(qb);

      await service.getAlerts({ acknowledged: false });

      expect(qb.andWhere).toHaveBeenCalledWith(
        'alert."acknowledgedAt" IS NULL',
      );
    });

    it('should respect custom page and pageSize', async () => {
      const qb = createQueryBuilderMock({ getMany: [], getCount: 50 });
      alertRepo.createQueryBuilder.mockReturnValue(qb);

      const result = await service.getAlerts({ page: 3, pageSize: 10 });

      expect(qb.skip).toHaveBeenCalledWith(20); // (3 - 1) * 10
      expect(qb.take).toHaveBeenCalledWith(10);
      expect(result.page).toBe(3);
      expect(result.pageSize).toBe(10);
      expect(result.total).toBe(50);
    });
  });

  // ── getAlert ────────────────────────────────────────────────────

  describe('getAlert', () => {
    it('should return the alert when found', async () => {
      const alert = { id: 'alert-1', title: 'Found alert' };
      alertRepo.findOne.mockResolvedValue(alert);

      const result = await service.getAlert('alert-1');

      expect(alertRepo.findOne).toHaveBeenCalledWith({
        where: { id: 'alert-1' },
      });
      expect(result).toEqual(alert);
    });

    it('should throw NotFoundException when alert is not found', async () => {
      alertRepo.findOne.mockResolvedValue(null);

      await expect(service.getAlert('missing-id')).rejects.toThrow(
        NotFoundException,
      );
    });
  });

  // ── acknowledgeAlert ────────────────────────────────────────────

  describe('acknowledgeAlert', () => {
    it('should set acknowledgedAt and acknowledgedBy then save', async () => {
      const existingAlert = {
        id: 'alert-1',
        title: 'Unacked',
        acknowledgedAt: null as Date | null,
        acknowledgedBy: null as string | null,
      };

      alertRepo.findOne.mockResolvedValue({ ...existingAlert });
      alertRepo.save.mockImplementation(async (entity) => entity);

      const result = await service.acknowledgeAlert('alert-1', 'user-99');

      expect(result.acknowledgedBy).toBe('user-99');
      expect(result.acknowledgedAt).toBeInstanceOf(Date);
      expect(alertRepo.save).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 'alert-1',
          acknowledgedBy: 'user-99',
        }),
      );
    });

    it('should throw NotFoundException when alert does not exist', async () => {
      alertRepo.findOne.mockResolvedValue(null);

      await expect(
        service.acknowledgeAlert('nonexistent', 'user-1'),
      ).rejects.toThrow(NotFoundException);
    });
  });

  // ── resolveAlert ────────────────────────────────────────────────

  describe('resolveAlert', () => {
    it('should set resolvedAt and save', async () => {
      const existingAlert = {
        id: 'alert-1',
        title: 'Unresolved',
        resolvedAt: null as Date | null,
      };

      alertRepo.findOne.mockResolvedValue({ ...existingAlert });
      alertRepo.save.mockImplementation(async (entity) => entity);

      const result = await service.resolveAlert('alert-1');

      expect(result.resolvedAt).toBeInstanceOf(Date);
      expect(alertRepo.save).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 'alert-1',
        }),
      );
    });

    it('should throw NotFoundException when alert does not exist', async () => {
      alertRepo.findOne.mockResolvedValue(null);

      await expect(service.resolveAlert('nonexistent')).rejects.toThrow(
        NotFoundException,
      );
    });
  });

  // ── createAlert ─────────────────────────────────────────────────

  describe('createAlert', () => {
    it('should save an alert and emit a Kafka event for anomaly alerts', async () => {
      const savedAlert = {
        id: 'alert-new-1',
        alertType: AlertType.SPEED_ANOMALY,
        severity: AlertSeverity.HIGH,
        title: 'Speed alert',
        description: 'Too fast',
        entityId: 'entity-1',
        ruleId: 'rule-1',
        metadata: {},
        createdAt: new Date('2025-06-01T00:00:00Z'),
      };

      alertRepo.create.mockReturnValue(savedAlert);
      alertRepo.save.mockResolvedValue(savedAlert);

      const result = await service.createAlert(
        AlertType.SPEED_ANOMALY,
        AlertSeverity.HIGH,
        'Speed alert',
        'Too fast',
        'entity-1',
        undefined,
        'rule-1',
      );

      expect(alertRepo.create).toHaveBeenCalledWith({
        alertType: AlertType.SPEED_ANOMALY,
        severity: AlertSeverity.HIGH,
        title: 'Speed alert',
        description: 'Too fast',
        entityId: 'entity-1',
        ruleId: 'rule-1',
        metadata: {},
      });
      expect(alertRepo.save).toHaveBeenCalledWith(savedAlert);
      expect(kafkaClient.emit).toHaveBeenCalledWith('alerts.anomaly', {
        key: 'entity-1',
        value: expect.objectContaining({
          alertId: 'alert-new-1',
          alertType: AlertType.SPEED_ANOMALY,
          severity: AlertSeverity.HIGH,
          entityId: 'entity-1',
        }),
      });
      expect(result).toEqual(savedAlert);
    });

    it('should emit to alerts.geofence topic for geofence alerts', async () => {
      const savedAlert = {
        id: 'alert-geo-1',
        alertType: AlertType.GEOFENCE_ENTRY,
        severity: AlertSeverity.CRITICAL,
        title: 'Geofence entry',
        description: 'Entered zone',
        entityId: 'entity-2',
        ruleId: 'rule-2',
        metadata: {},
        createdAt: new Date(),
      };

      alertRepo.create.mockReturnValue(savedAlert);
      alertRepo.save.mockResolvedValue(savedAlert);

      await service.createAlert(
        AlertType.GEOFENCE_ENTRY,
        AlertSeverity.CRITICAL,
        'Geofence entry',
        'Entered zone',
        'entity-2',
        { lat: 10, lng: 20 },
        'rule-2',
      );

      expect(kafkaClient.emit).toHaveBeenCalledWith(
        'alerts.geofence',
        expect.objectContaining({
          key: 'entity-2',
          value: expect.objectContaining({
            alertType: AlertType.GEOFENCE_ENTRY,
            position: { lat: 10, lng: 20 },
          }),
        }),
      );
    });

    it('should update position via raw SQL when position is provided', async () => {
      const savedAlert = {
        id: 'alert-pos-1',
        alertType: AlertType.GEOFENCE_EXIT,
        severity: AlertSeverity.MEDIUM,
        title: 'Geofence exit',
        description: 'Exited zone',
        entityId: 'entity-3',
        metadata: {},
        createdAt: new Date(),
      };

      alertRepo.create.mockReturnValue(savedAlert);
      alertRepo.save.mockResolvedValue(savedAlert);

      await service.createAlert(
        AlertType.GEOFENCE_EXIT,
        AlertSeverity.MEDIUM,
        'Geofence exit',
        'Exited zone',
        'entity-3',
        { lat: 40.7128, lng: -74.006 },
      );

      expect(alertRepo.query).toHaveBeenCalledWith(
        expect.stringContaining('UPDATE sentinel.alerts SET position'),
        [-74.006, 40.7128, 'alert-pos-1'],
      );
    });

    it('should not update position when none is provided', async () => {
      const savedAlert = {
        id: 'alert-nopos',
        alertType: AlertType.SPEED_ANOMALY,
        severity: AlertSeverity.LOW,
        title: 'Speed',
        description: 'Slow',
        entityId: 'entity-4',
        metadata: {},
        createdAt: new Date(),
      };

      alertRepo.create.mockReturnValue(savedAlert);
      alertRepo.save.mockResolvedValue(savedAlert);

      await service.createAlert(
        AlertType.SPEED_ANOMALY,
        AlertSeverity.LOW,
        'Speed',
        'Slow',
        'entity-4',
      );

      expect(alertRepo.query).not.toHaveBeenCalled();
    });

    it('should pass metadata to the created alert record', async () => {
      const savedAlert = {
        id: 'alert-meta',
        alertType: AlertType.CUSTOM,
        severity: AlertSeverity.LOW,
        title: 'Custom',
        description: 'Custom alert',
        entityId: 'entity-5',
        metadata: { source: 'manual', priority: 1 },
        createdAt: new Date(),
      };

      alertRepo.create.mockReturnValue(savedAlert);
      alertRepo.save.mockResolvedValue(savedAlert);

      await service.createAlert(
        AlertType.CUSTOM,
        AlertSeverity.LOW,
        'Custom',
        'Custom alert',
        'entity-5',
        undefined,
        undefined,
        { source: 'manual', priority: 1 },
      );

      expect(alertRepo.create).toHaveBeenCalledWith(
        expect.objectContaining({
          metadata: { source: 'manual', priority: 1 },
        }),
      );
    });
  });
});
