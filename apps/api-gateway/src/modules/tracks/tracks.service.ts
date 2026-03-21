import { Injectable, Logger, ServiceUnavailableException } from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { ConfigService } from '@nestjs/config';
import { firstValueFrom } from 'rxjs';

@Injectable()
export class TracksService {
  private readonly logger = new Logger(TracksService.name);
  private readonly trackServiceUrl: string;

  constructor(
    private readonly httpService: HttpService,
    private readonly configService: ConfigService,
  ) {
    this.trackServiceUrl = this.configService.get<string>(
      'TRACK_SERVICE_URL',
      'http://localhost:3002',
    );
  }

  async getHistory(entityId: string, query: Record<string, any>): Promise<any[]> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.get(`${this.trackServiceUrl}/tracks/${entityId}`, { params: query }),
      );
      return data;
    } catch (err) {
      this.logger.warn(`Track service error: ${err instanceof Error ? err.message : String(err)}`);
      throw new ServiceUnavailableException('Track service unavailable');
    }
  }

  async getLatestPosition(entityId: string): Promise<any> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.get(`${this.trackServiceUrl}/tracks/${entityId}/latest`),
      );
      return data;
    } catch (err) {
      this.logger.warn(`Track service error: ${err instanceof Error ? err.message : String(err)}`);
      throw new ServiceUnavailableException('Track service unavailable');
    }
  }

  async getSegments(entityId: string, query: Record<string, any>): Promise<any[]> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.get(`${this.trackServiceUrl}/tracks/${entityId}/segments`, { params: query }),
      );
      return data;
    } catch (err) {
      this.logger.warn(`Track service error: ${err instanceof Error ? err.message : String(err)}`);
      throw new ServiceUnavailableException('Track service unavailable');
    }
  }

  getReplayStreamUrl(entityId: string): string {
    return `${this.trackServiceUrl}/tracks/${entityId}/replay-stream`;
  }
}
