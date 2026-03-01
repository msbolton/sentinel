import { Test, TestingModule } from '@nestjs/testing';
import { AlertController } from './alert.controller';
import { AlertService } from './alert.service';
import { AlertSeverity, AlertType, RuleType } from './alert-type.enum';
import { QueryAlertDto, AcknowledgeAlertDto } from './dto/query-alert.dto';
import { CreateAlertRuleDto, UpdateAlertRuleDto } from './dto/create-alert-rule.dto';

describe('AlertController', () => {
  let controller: AlertController;
  let alertService: jest.Mocked<AlertService>;

  beforeEach(async () => {
    const mockAlertService: Partial<Record<keyof AlertService, jest.Mock>> = {
      getAlerts: jest.fn(),
      getAlert: jest.fn(),
      acknowledgeAlert: jest.fn(),
      resolveAlert: jest.fn(),
      createRule: jest.fn(),
      getRules: jest.fn(),
      updateRule: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      controllers: [AlertController],
      providers: [
        {
          provide: AlertService,
          useValue: mockAlertService,
        },
      ],
    }).compile();

    controller = module.get<AlertController>(AlertController);
    alertService = module.get(AlertService) as jest.Mocked<AlertService>;
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  // ── Alerts ──────────────────────────────────────────────────────

  describe('getAlerts', () => {
    it('should delegate to alertService.getAlerts with correct filters', async () => {
      const query: QueryAlertDto = {
        severity: AlertSeverity.HIGH,
        types: [AlertType.SPEED_ANOMALY],
        entityId: 'entity-123',
        acknowledged: false,
        page: 2,
        pageSize: 10,
      };

      const expected = {
        data: [],
        total: 0,
        page: 2,
        pageSize: 10,
      };

      alertService.getAlerts.mockResolvedValue(expected);

      const result = await controller.getAlerts(query);

      expect(alertService.getAlerts).toHaveBeenCalledWith({
        severity: AlertSeverity.HIGH,
        types: [AlertType.SPEED_ANOMALY],
        entityId: 'entity-123',
        acknowledged: false,
        page: 2,
        pageSize: 10,
      });
      expect(result).toEqual(expected);
    });

    it('should pass undefined for optional filters that are not provided', async () => {
      const query: QueryAlertDto = {};

      alertService.getAlerts.mockResolvedValue({
        data: [],
        total: 0,
        page: 1,
        pageSize: 20,
      });

      await controller.getAlerts(query);

      expect(alertService.getAlerts).toHaveBeenCalledWith({
        severity: undefined,
        types: undefined,
        entityId: undefined,
        acknowledged: undefined,
        page: undefined,
        pageSize: undefined,
      });
    });
  });

  describe('getAlert', () => {
    it('should delegate to alertService.getAlert with the alert id', async () => {
      const alertId = '550e8400-e29b-41d4-a716-446655440000';
      const expected = { id: alertId, title: 'Test Alert' } as any;

      alertService.getAlert.mockResolvedValue(expected);

      const result = await controller.getAlert(alertId);

      expect(alertService.getAlert).toHaveBeenCalledWith(alertId);
      expect(result).toEqual(expected);
    });
  });

  describe('acknowledgeAlert', () => {
    it('should delegate to alertService.acknowledgeAlert with id and userId', async () => {
      const alertId = '550e8400-e29b-41d4-a716-446655440000';
      const dto: AcknowledgeAlertDto = { userId: 'user-42' };
      const expected = {
        id: alertId,
        acknowledgedAt: new Date(),
        acknowledgedBy: 'user-42',
      } as any;

      alertService.acknowledgeAlert.mockResolvedValue(expected);

      const result = await controller.acknowledgeAlert(alertId, dto);

      expect(alertService.acknowledgeAlert).toHaveBeenCalledWith(alertId, 'user-42');
      expect(result).toEqual(expected);
    });
  });

  describe('resolveAlert', () => {
    it('should delegate to alertService.resolveAlert with the alert id', async () => {
      const alertId = '550e8400-e29b-41d4-a716-446655440000';
      const expected = { id: alertId, resolvedAt: new Date() } as any;

      alertService.resolveAlert.mockResolvedValue(expected);

      const result = await controller.resolveAlert(alertId);

      expect(alertService.resolveAlert).toHaveBeenCalledWith(alertId);
      expect(result).toEqual(expected);
    });
  });

  // ── Rules ───────────────────────────────────────────────────────

  describe('createRule', () => {
    it('should delegate to alertService.createRule with the dto', async () => {
      const dto: CreateAlertRuleDto = {
        name: 'Max Speed Rule',
        ruleType: RuleType.SPEED_THRESHOLD,
        config: { maxSpeedKnots: 50 },
        monitoredEntityTypes: ['vessel'],
        severity: AlertSeverity.HIGH,
        enabled: true,
      };

      const expected = { id: 'rule-1', ...dto } as any;
      alertService.createRule.mockResolvedValue(expected);

      const result = await controller.createRule(dto);

      expect(alertService.createRule).toHaveBeenCalledWith(dto);
      expect(result).toEqual(expected);
    });
  });

  describe('getRules', () => {
    it('should delegate to alertService.getRules', async () => {
      const expected = [{ id: 'rule-1', name: 'Rule A' }] as any;
      alertService.getRules.mockResolvedValue(expected);

      const result = await controller.getRules();

      expect(alertService.getRules).toHaveBeenCalledWith();
      expect(result).toEqual(expected);
    });
  });

  describe('updateRule', () => {
    it('should delegate to alertService.updateRule with id and dto', async () => {
      const ruleId = '550e8400-e29b-41d4-a716-446655440000';
      const dto: UpdateAlertRuleDto = {
        name: 'Updated Rule Name',
        enabled: false,
      };

      const expected = { id: ruleId, ...dto } as any;
      alertService.updateRule.mockResolvedValue(expected);

      const result = await controller.updateRule(ruleId, dto);

      expect(alertService.updateRule).toHaveBeenCalledWith(ruleId, dto);
      expect(result).toEqual(expected);
    });
  });
});
