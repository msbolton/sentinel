import { Injectable, Logger, OnModuleDestroy } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import Redis from 'ioredis';

/**
 * Bounding box defining a client's visible map viewport.
 */
export interface ViewportBounds {
  north: number;
  south: number;
  east: number;
  west: number;
}

/**
 * Redis key prefix for viewport storage.
 */
const VIEWPORT_KEY_PREFIX = 'sentinel:viewports';

/**
 * Manages client viewport registrations in Redis.
 *
 * Each connected WebSocket client has a viewport bounding box stored
 * as a Redis HASH. This enables efficient point-in-viewport lookups
 * to determine which clients should receive a given entity update.
 *
 * Using Redis (rather than in-memory storage) allows the gateway to
 * scale horizontally across multiple instances while sharing viewport
 * state.
 */
@Injectable()
export class ViewportService implements OnModuleDestroy {
  private readonly logger = new Logger(ViewportService.name);
  private readonly redis: Redis;

  /**
   * In-memory cache of viewports for low-latency lookups.
   * Synced with Redis on write operations. This avoids a Redis round-trip
   * on every single entity broadcast (which can be thousands per second).
   */
  private readonly localCache = new Map<string, ViewportBounds>();

  constructor(private readonly configService: ConfigService) {
    const redisHost = this.configService.get<string>('REDIS_HOST', 'localhost');
    const redisPort = this.configService.get<number>('REDIS_PORT', 6379);
    const redisPassword = this.configService.get<string>('REDIS_PASSWORD', '');

    this.redis = new Redis({
      host: redisHost,
      port: redisPort,
      password: redisPassword || undefined,
      keyPrefix: '',
      retryStrategy: (times: number) => {
        if (times > 10) {
          this.logger.error('Redis connection failed after 10 retries');
          return null;
        }
        return Math.min(times * 200, 5000);
      },
      lazyConnect: true,
    });

    this.redis.on('connect', () => {
      this.logger.log(`Connected to Redis at ${redisHost}:${redisPort}`);
    });

    this.redis.on('error', (err) => {
      this.logger.error(`Redis error: ${err.message}`);
    });

    // Connect asynchronously - don't block constructor
    this.redis.connect().catch((err) => {
      this.logger.warn(`Redis initial connection deferred: ${err.message}`);
    });
  }

  async onModuleDestroy(): Promise<void> {
    await this.redis.quit();
  }

  /**
   * Stores a client's viewport bounding box in both Redis and local cache.
   */
  async setViewport(
    clientId: string,
    bounds: ViewportBounds,
  ): Promise<void> {
    const key = `${VIEWPORT_KEY_PREFIX}:${clientId}`;

    try {
      await this.redis.hset(key, {
        north: bounds.north.toString(),
        south: bounds.south.toString(),
        east: bounds.east.toString(),
        west: bounds.west.toString(),
      });

      // Set TTL to auto-cleanup stale viewports (e.g., crashed clients)
      await this.redis.expire(key, 3600);

      // Update local cache
      this.localCache.set(clientId, { ...bounds });
    } catch (error) {
      this.logger.error(
        `Failed to set viewport for ${clientId}: ${error}`,
      );
      // Still update local cache as fallback
      this.localCache.set(clientId, { ...bounds });
    }
  }

  /**
   * Removes a client's viewport from Redis and local cache.
   */
  async removeViewport(clientId: string): Promise<void> {
    const key = `${VIEWPORT_KEY_PREFIX}:${clientId}`;

    try {
      await this.redis.del(key);
    } catch (error) {
      this.logger.error(
        `Failed to remove viewport for ${clientId}: ${error}`,
      );
    }

    this.localCache.delete(clientId);
  }

  /**
   * Retrieves a client's viewport, preferring local cache and falling
   * back to Redis.
   */
  async getViewport(clientId: string): Promise<ViewportBounds | null> {
    // Check local cache first
    const cached = this.localCache.get(clientId);
    if (cached) {
      return cached;
    }

    // Fall back to Redis
    try {
      const key = `${VIEWPORT_KEY_PREFIX}:${clientId}`;
      const data = await this.redis.hgetall(key);

      if (!data || !data['north']) {
        return null;
      }

      const bounds: ViewportBounds = {
        north: parseFloat(data['north']),
        south: parseFloat(data['south']),
        east: parseFloat(data['east']),
        west: parseFloat(data['west']),
      };

      // Populate local cache
      this.localCache.set(clientId, bounds);

      return bounds;
    } catch (error) {
      this.logger.error(
        `Failed to get viewport for ${clientId}: ${error}`,
      );
      return null;
    }
  }

  /**
   * Checks whether a geographic point (lat, lng) falls within a client's
   * registered viewport bounding box.
   *
   * Handles the antimeridian (date line) crossing case where west > east.
   */
  async isEntityInViewport(
    clientId: string,
    latitude: number,
    longitude: number,
  ): Promise<boolean> {
    const viewport = await this.getViewport(clientId);
    if (!viewport) {
      return false;
    }

    return this.isPointInBounds(latitude, longitude, viewport);
  }

  /**
   * Returns the list of client IDs whose viewport contains the given
   * geographic point. Used for efficient broadcast targeting.
   */
  async getClientsForEntity(
    latitude: number,
    longitude: number,
  ): Promise<string[]> {
    const matchingClients: string[] = [];

    for (const [clientId, viewport] of this.localCache) {
      if (this.isPointInBounds(latitude, longitude, viewport)) {
        matchingClients.push(clientId);
      }
    }

    return matchingClients;
  }

  /**
   * Pure geometric check: is the point within the bounding box?
   * Handles antimeridian-crossing viewports where west > east.
   */
  private isPointInBounds(
    latitude: number,
    longitude: number,
    bounds: ViewportBounds,
  ): boolean {
    // Latitude check is straightforward
    if (latitude > bounds.north || latitude < bounds.south) {
      return false;
    }

    // Longitude check must handle antimeridian crossing
    if (bounds.west <= bounds.east) {
      // Normal case: viewport does not cross the antimeridian
      return longitude >= bounds.west && longitude <= bounds.east;
    } else {
      // Antimeridian crossing: viewport wraps around 180/-180
      return longitude >= bounds.west || longitude <= bounds.east;
    }
  }
}
