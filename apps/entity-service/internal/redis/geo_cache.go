package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const geoKey = "sentinel:entities:geo"

// GeoCache provides optional Redis-backed geospatial indexing.
type GeoCache struct {
	client  *redis.Client
	logger  *zap.Logger
	enabled bool
}

// NewGeoCache creates a new geo cache. If Redis is unreachable, the cache is disabled.
func NewGeoCache(host, port, password string, logger *zap.Logger) *GeoCache {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port),
		Password: password,
		DB:       0,
	})

	gc := &GeoCache{client: client, logger: logger}

	if err := client.Ping(context.Background()).Err(); err != nil {
		logger.Warn("redis unavailable, geo cache disabled", zap.Error(err))
		gc.enabled = false
	} else {
		logger.Info("redis connected, geo cache enabled")
		gc.enabled = true
	}

	return gc
}

// IsEnabled returns whether the geo cache is active.
func (g *GeoCache) IsEnabled() bool {
	return g.enabled
}

// Add adds or updates an entity in the geo index.
func (g *GeoCache) Add(ctx context.Context, entityID string, lng, lat float64) {
	if !g.enabled {
		return
	}
	if err := g.client.GeoAdd(ctx, geoKey, &redis.GeoLocation{
		Name:      entityID,
		Longitude: lng,
		Latitude:  lat,
	}).Err(); err != nil {
		g.logger.Warn("geo cache add failed", zap.String("entityId", entityID), zap.Error(err))
	}
}

// Remove removes an entity from the geo index.
func (g *GeoCache) Remove(ctx context.Context, entityID string) {
	if !g.enabled {
		return
	}
	if err := g.client.ZRem(ctx, geoKey, entityID).Err(); err != nil {
		g.logger.Warn("geo cache remove failed", zap.String("entityId", entityID), zap.Error(err))
	}
}

// Flush deletes the entire geo index.
func (g *GeoCache) Flush(ctx context.Context) {
	if !g.enabled {
		return
	}
	if err := g.client.Del(ctx, geoKey).Err(); err != nil {
		g.logger.Warn("geo cache flush failed", zap.Error(err))
	}
}

// Close closes the Redis client.
func (g *GeoCache) Close() error {
	return g.client.Close()
}
