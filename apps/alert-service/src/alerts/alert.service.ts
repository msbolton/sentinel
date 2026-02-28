import {
  Injectable,
  Inject,
  Logger,
  NotFoundException,
} from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository } from 'typeorm';
import { ClientKafka } from '@nestjs/microservices';
import { AlertRecord } from './alert.entity';
import { AlertRuleRecord } from './alert-rule.entity';
import {
  AlertType,
  AlertSeverity,
  RuleType,
} from './alert-type.enum';
import { CreateAlertRuleDto, UpdateAlertRuleDto } from './dto/create-alert-rule.dto';

@Injectable()
export class AlertService {
  private readonly logger = new Logger(AlertService.name);

  constructor(
    @InjectRepository(AlertRecord)
    private readonly alertRepo: Repository<AlertRecord>,
    @InjectRepository(AlertRuleRecord)
    private readonly ruleRepo: Repository<AlertRuleRecord>,
    @Inject('KAFKA_SERVICE')
    private readonly kafkaClient: ClientKafka,
  ) {}

  // ── Rule Management ───────────────────────────────────────────

  /**
   * Create a new alert rule.
   */
  async createRule(dto: CreateAlertRuleDto): Promise<AlertRuleRecord> {
    const rule = this.ruleRepo.create({
      name: dto.name,
      ruleType: dto.ruleType,
      config: dto.config,
      monitoredEntityTypes: dto.monitoredEntityTypes || [],
      severity: dto.severity ?? AlertSeverity.MEDIUM,
      enabled: dto.enabled ?? true,
    });

    return this.ruleRepo.save(rule);
  }

  /**
   * List all alert rules.
   */
  async getRules(): Promise<AlertRuleRecord[]> {
    return this.ruleRepo.find({
      order: { createdAt: 'DESC' },
    });
  }

  /**
   * Update an existing alert rule.
   */
  async updateRule(
    id: string,
    dto: UpdateAlertRuleDto,
  ): Promise<AlertRuleRecord> {
    const rule = await this.ruleRepo.findOne({ where: { id } });
    if (!rule) {
      throw new NotFoundException(`Alert rule ${id} not found`);
    }

    Object.assign(rule, dto);
    return this.ruleRepo.save(rule);
  }

  // ── Evaluation ────────────────────────────────────────────────

  /**
   * Evaluate all geofence rules against a position update.
   * Uses PostGIS ST_Contains to check if position falls within geofence polygons.
   */
  async evaluateEntityPosition(
    entityId: string,
    lat: number,
    lng: number,
    entityType: string,
  ): Promise<AlertRecord[]> {
    const rules = await this.getEnabledRules(RuleType.GEOFENCE, entityType);
    const alerts: AlertRecord[] = [];

    for (const rule of rules) {
      const config = rule.config as {
        polygon: number[][];
        triggerOn?: string;
      };

      if (!config.polygon || config.polygon.length < 3) {
        continue;
      }

      // Build PostGIS polygon from config coordinates
      const polygonCoords = config.polygon
        .map((coord) => `${coord[0]} ${coord[1]}`)
        .join(', ');
      // Close the polygon ring
      const firstCoord = config.polygon[0];
      const closedPolygon = `${polygonCoords}, ${firstCoord[0]} ${firstCoord[1]}`;

      const sql = `
        SELECT ST_Contains(
          ST_GeomFromText('POLYGON((${closedPolygon}))', 4326),
          ST_SetSRID(ST_MakePoint($1, $2), 4326)
        ) AS inside
      `;

      const result = await this.alertRepo.query(sql, [lng, lat]);
      const isInside = result[0]?.inside === true;

      const triggerOn = config.triggerOn || 'ENTRY';
      const shouldAlert =
        (triggerOn === 'ENTRY' && isInside) ||
        (triggerOn === 'EXIT' && !isInside) ||
        triggerOn === 'BOTH';

      if (shouldAlert) {
        const alertType = isInside
          ? AlertType.GEOFENCE_ENTRY
          : AlertType.GEOFENCE_EXIT;

        const alert = await this.createAlert(
          alertType,
          rule.severity,
          `${alertType === AlertType.GEOFENCE_ENTRY ? 'Entered' : 'Exited'} geofence: ${rule.name}`,
          `Entity ${entityId} ${isInside ? 'entered' : 'exited'} geofence zone "${rule.name}"`,
          entityId,
          { lat, lng },
          rule.id,
        );

        alerts.push(alert);
      }
    }

    return alerts;
  }

  /**
   * Evaluate speed anomaly rules against a speed observation.
   */
  async evaluateSpeedAnomaly(
    entityId: string,
    speedKnots: number,
    entityType: string,
  ): Promise<AlertRecord[]> {
    const rules = await this.getEnabledRules(
      RuleType.SPEED_THRESHOLD,
      entityType,
    );
    const alerts: AlertRecord[] = [];

    for (const rule of rules) {
      const config = rule.config as {
        maxSpeedKnots?: number;
        minSpeedKnots?: number;
      };

      let violation = false;
      let description = '';

      if (
        config.maxSpeedKnots !== undefined &&
        speedKnots > config.maxSpeedKnots
      ) {
        violation = true;
        description = `Entity ${entityId} speed ${speedKnots} kts exceeds maximum ${config.maxSpeedKnots} kts (rule: ${rule.name})`;
      }

      if (
        config.minSpeedKnots !== undefined &&
        speedKnots < config.minSpeedKnots
      ) {
        violation = true;
        description = `Entity ${entityId} speed ${speedKnots} kts below minimum ${config.minSpeedKnots} kts (rule: ${rule.name})`;
      }

      if (violation) {
        const alert = await this.createAlert(
          AlertType.SPEED_ANOMALY,
          rule.severity,
          `Speed anomaly detected: ${rule.name}`,
          description,
          entityId,
          undefined,
          rule.id,
        );

        alerts.push(alert);
      }
    }

    return alerts;
  }

  // ── Alert CRUD ────────────────────────────────────────────────

  /**
   * Create a new alert and publish to Kafka.
   */
  async createAlert(
    alertType: AlertType,
    severity: AlertSeverity,
    title: string,
    description: string,
    entityId: string,
    position?: { lat: number; lng: number },
    ruleId?: string,
    metadata?: Record<string, unknown>,
  ): Promise<AlertRecord> {
    const alert = this.alertRepo.create({
      alertType,
      severity,
      title,
      description,
      entityId,
      ruleId: ruleId ?? undefined,
      metadata: metadata || {},
    });

    // Set position using raw query after save if position provided
    const savedAlert = await this.alertRepo.save(alert);

    if (position) {
      await this.alertRepo.query(
        `UPDATE sentinel.alerts SET position = ST_SetSRID(ST_MakePoint($1, $2), 4326) WHERE id = $3`,
        [position.lng, position.lat, savedAlert.id],
      );
    }

    // Publish alert to Kafka
    const kafkaTopic =
      alertType === AlertType.GEOFENCE_ENTRY ||
      alertType === AlertType.GEOFENCE_EXIT
        ? 'alerts.geofence'
        : 'alerts.anomaly';

    this.kafkaClient.emit(kafkaTopic, {
      key: entityId,
      value: {
        alertId: savedAlert.id,
        alertType,
        severity,
        title,
        description,
        entityId,
        position,
        ruleId,
        createdAt: savedAlert.createdAt,
      },
    });

    this.logger.log(
      `Alert created: [${severity}] ${title} (entity: ${entityId})`,
    );

    return savedAlert;
  }

  /**
   * Query alerts with filters and pagination.
   */
  async getAlerts(filters: {
    severity?: AlertSeverity;
    types?: AlertType[];
    entityId?: string;
    acknowledged?: boolean;
    page?: number;
    pageSize?: number;
  }): Promise<{ data: AlertRecord[]; total: number; page: number; pageSize: number }> {
    const page = filters.page || 1;
    const pageSize = filters.pageSize || 20;

    const queryBuilder = this.alertRepo
      .createQueryBuilder('alert')
      .orderBy('alert.createdAt', 'DESC');

    if (filters.severity) {
      queryBuilder.andWhere('alert.severity = :severity', {
        severity: filters.severity,
      });
    }

    if (filters.types && filters.types.length > 0) {
      queryBuilder.andWhere('alert."alertType" IN (:...types)', {
        types: filters.types,
      });
    }

    if (filters.entityId) {
      queryBuilder.andWhere('alert."entityId" = :entityId', {
        entityId: filters.entityId,
      });
    }

    if (filters.acknowledged === true) {
      queryBuilder.andWhere('alert."acknowledgedAt" IS NOT NULL');
    } else if (filters.acknowledged === false) {
      queryBuilder.andWhere('alert."acknowledgedAt" IS NULL');
    }

    const total = await queryBuilder.getCount();

    const data = await queryBuilder
      .skip((page - 1) * pageSize)
      .take(pageSize)
      .getMany();

    return { data, total, page, pageSize };
  }

  /**
   * Get a single alert by ID.
   */
  async getAlert(id: string): Promise<AlertRecord> {
    const alert = await this.alertRepo.findOne({ where: { id } });
    if (!alert) {
      throw new NotFoundException(`Alert ${id} not found`);
    }
    return alert;
  }

  /**
   * Mark an alert as acknowledged.
   */
  async acknowledgeAlert(alertId: string, userId: string): Promise<AlertRecord> {
    const alert = await this.getAlert(alertId);

    alert.acknowledgedAt = new Date();
    alert.acknowledgedBy = userId;

    return this.alertRepo.save(alert);
  }

  /**
   * Mark an alert as resolved.
   */
  async resolveAlert(alertId: string): Promise<AlertRecord> {
    const alert = await this.getAlert(alertId);

    alert.resolvedAt = new Date();

    return this.alertRepo.save(alert);
  }

  // ── Helpers ───────────────────────────────────────────────────

  /**
   * Get enabled rules of a specific type, optionally filtered by entity type.
   */
  private async getEnabledRules(
    ruleType: RuleType,
    entityType?: string,
  ): Promise<AlertRuleRecord[]> {
    const queryBuilder = this.ruleRepo
      .createQueryBuilder('rule')
      .where('rule.enabled = :enabled', { enabled: true })
      .andWhere('rule."ruleType" = :ruleType', { ruleType });

    const rules = await queryBuilder.getMany();

    // Filter by entity type if specified
    if (entityType) {
      return rules.filter(
        (rule) =>
          rule.monitoredEntityTypes.length === 0 ||
          rule.monitoredEntityTypes.includes(entityType),
      );
    }

    return rules;
  }
}
