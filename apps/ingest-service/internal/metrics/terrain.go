package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sentinel/go-common/terrain"
)

// terrainCollector exports the terrain service's own counters, read when
// Prometheus scrapes (the service keeps them; nothing is copied per lookup).
type terrainCollector struct {
	stats                                        func() terrain.Stats
	hits, misses, downloads, failures, cacheSize *prometheus.Desc
}

// NewTerrainCollector exports a terrain service's Stats as
// sentinel_ingest_terrain_{hits,misses,downloads,failures}_total and
// sentinel_ingest_terrain_cache_bytes. Register it only when terrain is on.
func NewTerrainCollector(stats func() terrain.Stats) prometheus.Collector {
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName("sentinel", "ingest", name), help, nil, nil)
	}
	return &terrainCollector{
		stats:     stats,
		hits:      desc("terrain_hits_total", "Terrain height lookups answered from the decoded tiles in memory."),
		misses:    desc("terrain_misses_total", "Terrain height lookups that needed a tile read from disk or downloaded."),
		downloads: desc("terrain_downloads_total", "Terrain tile downloads completed."),
		failures:  desc("terrain_failures_total", "Terrain tile fetches or decodes that failed."),
		cacheSize: desc("terrain_cache_bytes", "Size of the terrain tiles in the cache folder."),
	}
}

func (c *terrainCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.hits, c.misses, c.downloads, c.failures, c.cacheSize} {
		ch <- d
	}
}

func (c *terrainCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stats()
	ch <- prometheus.MustNewConstMetric(c.hits, prometheus.CounterValue, float64(s.Hits))
	ch <- prometheus.MustNewConstMetric(c.misses, prometheus.CounterValue, float64(s.Misses))
	ch <- prometheus.MustNewConstMetric(c.downloads, prometheus.CounterValue, float64(s.Downloads))
	ch <- prometheus.MustNewConstMetric(c.failures, prometheus.CounterValue, float64(s.Failures))
	ch <- prometheus.MustNewConstMetric(c.cacheSize, prometheus.GaugeValue, float64(s.CacheBytes))
}
