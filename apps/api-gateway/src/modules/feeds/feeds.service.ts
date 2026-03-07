import { Injectable, Logger, ServiceUnavailableException } from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { ConfigService } from '@nestjs/config';
import { firstValueFrom } from 'rxjs';

export interface FeedHealth {
  lastSuccessAt: string;
  entitiesCount: number;
  errorCount: number;
  status: 'healthy' | 'warn' | 'critical' | 'unknown';
}

export interface FeedStatus {
  id: string;
  name: string;
  sourceType: string;
  description: string;
  enabled: boolean;
  health?: FeedHealth;
}

@Injectable()
export class FeedsService {
  private readonly logger = new Logger(FeedsService.name);
  private readonly ingestUrl: string;

  constructor(
    private readonly httpService: HttpService,
    private readonly configService: ConfigService,
  ) {
    this.ingestUrl = this.configService.get<string>(
      'INGEST_SERVICE_URL',
      'http://localhost:4000',
    );
  }

  async listFeeds(): Promise<FeedStatus[]> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.get<FeedStatus[]>(`${this.ingestUrl}/feeds`),
      );
      return data;
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      this.logger.warn(
        `Ingest service unreachable at ${this.ingestUrl}: ${msg}`,
      );
      throw new ServiceUnavailableException('Ingest service unavailable');
    }
  }

  async toggleFeed(id: string, enabled: boolean): Promise<FeedStatus> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.put<FeedStatus>(`${this.ingestUrl}/feeds/${id}`, {
          enabled,
        }),
      );
      return data;
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      this.logger.warn(
        `Failed to toggle feed ${id}: ${msg}`,
      );
      throw new ServiceUnavailableException('Ingest service unavailable');
    }
  }
}
