package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sentinel/go-common/terrain"
)

// The terrain counters are read from the service when Prometheus scrapes.
func TestTerrainCollectorReadsStatsOnScrape(t *testing.T) {
	stats := terrain.Stats{Hits: 7, Misses: 3, Downloads: 2, Failures: 1, CacheBytes: 1 << 20}
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewTerrainCollector(func() terrain.Stats { return stats }))

	// read returns each series' counter value and gauge value; a family of
	// the wrong kind reads as -1 so the comparison below fails.
	type value struct{ counter, gauge float64 }
	read := func() map[string]value {
		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]value{}
		for _, f := range families {
			if len(f.GetMetric()) != 1 {
				t.Errorf("%s has %d series, want 1", f.GetName(), len(f.GetMetric()))
				continue
			}
			m := f.GetMetric()[0]
			v := value{-1, -1}
			if m.Counter != nil {
				v.counter = m.GetCounter().GetValue()
			}
			if m.Gauge != nil {
				v.gauge = m.GetGauge().GetValue()
			}
			out[f.GetName()] = v
		}
		return out
	}

	got := read()
	counters := map[string]float64{
		"sentinel_ingest_terrain_hits_total":      7,
		"sentinel_ingest_terrain_misses_total":    3,
		"sentinel_ingest_terrain_downloads_total": 2,
		"sentinel_ingest_terrain_failures_total":  1,
	}
	for name, want := range counters {
		if v, ok := got[name]; !ok || v.counter != want {
			t.Errorf("%s = %+v, want counter %v", name, v, want)
		}
	}
	if v := got["sentinel_ingest_terrain_cache_bytes"]; v.gauge != 1<<20 {
		t.Errorf("cache_bytes = %+v, want gauge %d", v, 1<<20)
	}

	stats.Hits, stats.CacheBytes = 9, 2<<20
	got = read()
	if v := got["sentinel_ingest_terrain_hits_total"].counter; v != 9 {
		t.Errorf("hits after a change = %v, want 9 (read at scrape time)", v)
	}
	if v := got["sentinel_ingest_terrain_cache_bytes"].gauge; v != 2<<20 {
		t.Errorf("cache_bytes after a change = %v", v)
	}
}
