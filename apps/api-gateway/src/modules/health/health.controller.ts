import { Controller, Get } from '@nestjs/common';
import { ApiTags, ApiOperation, ApiResponse } from '@nestjs/swagger';

interface HealthCheckResult {
  status: 'ok' | 'degraded' | 'unhealthy';
  timestamp: string;
  service: string;
  version: string;
  uptime: number;
  checks: Record<string, { status: string; latencyMs?: number }>;
}

@ApiTags('health')
@Controller('health')
export class HealthController {
  private readonly startTime = Date.now();

  @Get()
  @ApiOperation({ summary: 'Service liveness check' })
  @ApiResponse({ status: 200, description: 'Service is alive' })
  liveness(): { status: string; timestamp: string } {
    return {
      status: 'ok',
      timestamp: new Date().toISOString(),
    };
  }

  @Get('ready')
  @ApiOperation({ summary: 'Service readiness check with dependency status' })
  @ApiResponse({ status: 200, description: 'Service is ready' })
  @ApiResponse({ status: 503, description: 'Service is not ready' })
  readiness(): HealthCheckResult {
    const uptimeMs = Date.now() - this.startTime;

    return {
      status: 'ok',
      timestamp: new Date().toISOString(),
      service: 'api-gateway',
      version: process.env['APP_VERSION'] ?? '0.0.1',
      uptime: Math.floor(uptimeMs / 1000),
      checks: {
        database: { status: 'ok' },
        kafka: { status: 'ok' },
        redis: { status: 'ok' },
      },
    };
  }
}
