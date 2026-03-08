import { Injectable, Logger, HttpException, ServiceUnavailableException } from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { ConfigService } from '@nestjs/config';
import { firstValueFrom } from 'rxjs';
import { CreateFeedDto } from './dto/create-feed.dto';
import { UpdateFeedDto } from './dto/update-feed.dto';

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
  custom: boolean;
  connectorType?: string;
  format?: string;
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

  async createFeed(dto: CreateFeedDto): Promise<FeedStatus> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.post<FeedStatus>(`${this.ingestUrl}/feeds`, dto),
      );
      return data;
    } catch (err: unknown) {
      this.rethrowOrUnavailable(err, 'create feed');
      throw err; // unreachable but satisfies TS
    }
  }

  async deleteFeed(id: string): Promise<void> {
    try {
      await firstValueFrom(
        this.httpService.delete(`${this.ingestUrl}/feeds/${id}`),
      );
    } catch (err: unknown) {
      this.rethrowOrUnavailable(err, `delete feed ${id}`);
    }
  }

  async updateFeed(id: string, dto: UpdateFeedDto): Promise<FeedStatus> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.patch<FeedStatus>(`${this.ingestUrl}/feeds/${id}`, dto),
      );
      return data;
    } catch (err: unknown) {
      this.rethrowOrUnavailable(err, `update feed ${id}`);
      throw err;
    }
  }

  private rethrowOrUnavailable(err: unknown, action: string): never {
    // Forward HTTP errors from ingest service (400, 403, 404).
    if (err && typeof err === 'object' && 'response' in err) {
      const resp = (err as { response?: { status?: number; data?: string } }).response;
      if (resp?.status && resp.status >= 400 && resp.status < 500) {
        throw new HttpException(resp.data ?? action, resp.status);
      }
    }
    const msg = err instanceof Error ? err.message : String(err);
    this.logger.warn(`Failed to ${action}: ${msg}`);
    throw new ServiceUnavailableException('Ingest service unavailable');
  }
}
