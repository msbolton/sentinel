import {
  Controller,
  Get,
  Post,
  Put,
  Patch,
  Param,
  Query,
  Body,
  ParseUUIDPipe,
  HttpCode,
  HttpStatus,
} from '@nestjs/common';
import { AlertService } from './alert.service';
import { CreateAlertRuleDto, UpdateAlertRuleDto } from './dto/create-alert-rule.dto';
import { QueryAlertDto, AcknowledgeAlertDto } from './dto/query-alert.dto';

@Controller('alerts')
export class AlertController {
  constructor(private readonly alertService: AlertService) {}

  // ── Alerts ────────────────────────────────────────────────────

  /**
   * GET /alerts
   * Query alerts with filters: severity, types[], entityId, acknowledged, page, pageSize.
   */
  @Get()
  async getAlerts(@Query() query: QueryAlertDto) {
    return this.alertService.getAlerts({
      severity: query.severity,
      types: query.types,
      entityId: query.entityId,
      acknowledged: query.acknowledged,
      page: query.page,
      pageSize: query.pageSize,
    });
  }

  /**
   * GET /alerts/:id
   * Get a single alert by ID.
   */
  @Get(':id')
  async getAlert(@Param('id', ParseUUIDPipe) id: string) {
    return this.alertService.getAlert(id);
  }

  /**
   * PATCH /alerts/:id/acknowledge
   * Mark an alert as acknowledged by a user.
   */
  @Patch(':id/acknowledge')
  async acknowledgeAlert(
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: AcknowledgeAlertDto,
  ) {
    return this.alertService.acknowledgeAlert(id, dto.userId);
  }

  /**
   * PATCH /alerts/:id/resolve
   * Mark an alert as resolved.
   */
  @Patch(':id/resolve')
  @HttpCode(HttpStatus.OK)
  async resolveAlert(@Param('id', ParseUUIDPipe) id: string) {
    return this.alertService.resolveAlert(id);
  }

  // ── Rules ─────────────────────────────────────────────────────

  /**
   * POST /alerts/rules
   * Create a new alert rule.
   */
  @Post('rules')
  @HttpCode(HttpStatus.CREATED)
  async createRule(@Body() dto: CreateAlertRuleDto) {
    return this.alertService.createRule(dto);
  }

  /**
   * GET /alerts/rules
   * List all alert rules.
   */
  @Get('rules')
  async getRules() {
    return this.alertService.getRules();
  }

  /**
   * PUT /alerts/rules/:id
   * Update an alert rule.
   */
  @Put('rules/:id')
  async updateRule(
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: UpdateAlertRuleDto,
  ) {
    return this.alertService.updateRule(id, dto);
  }
}
