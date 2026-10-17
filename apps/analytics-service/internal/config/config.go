// Package config reads the analytics service's configuration from the
// environment: the usual service settings plus every threshold the baseline
// builder and the detectors use, so a deployment can tune them without a
// rebuild and every finding can record the values it was judged against.
package config

import (
	"strconv"
	"strings"
	"time"

	commonCfg "github.com/sentinel/go-common/config"
)

// Config holds the analytics-service configuration.
type Config struct {
	Port         string
	DatabaseURL  string
	KafkaBrokers string
	CORSOrigin   string

	Baseline  BaselineConfig
	Detectors DetectorConfig
}

// BaselineConfig tunes what "normal" is built from.
type BaselineConfig struct {
	Window        time.Duration // history used for a baseline
	Interval      time.Duration // how often baselines are recomputed
	ActiveWindow  time.Duration // entities with a fix this recently are recomputed
	MinPoints     int           // maturity gate: at least this many fixes
	MinDays       int           // maturity gate: spread over at least this many days
	CacheTTL      time.Duration // in-memory baseline cache
	Workers       int           // parallel recomputations
	ThinInterval  time.Duration // keep at most one fix per interval when loading a track
	MaxSlowPoints int           // stride-sample the slow set to this many points before clustering
	DBSCANEpsM    float64       // neighbourhood radius for dwell clustering
	DBSCANMinPts  int           // minimum neighbours for a core point
	SlowKnots     TypeMap       // a fix at or below this speed counts as dwelling
}

// DetectorConfig tunes each kind of finding.
type DetectorConfig struct {
	SkipTypes map[string]bool // entity types never judged (orbital, stationary)

	PlaceRangeFactor float64 // unusual place: distance > factor x range p95 ...
	PlaceMinM        TypeMap // ... and > this many metres

	TimeMinShare   float64       // unusual time: slot share below this ...
	TimeMinDays    int           // ... with at least this many days of history
	TimeQuietShare float64       // absence: a slot normally above this share ...
	TimeQuietGap   time.Duration // ... with no fix for this long

	MoveZ        float64 // unusual movement: z-score above this
	MoveMaxKnots TypeMap // impossible move: implied speed above this
	MoveMinGap   time.Duration

	DarkGapFactor   float64       // went dark: gap > factor x gap p95 ...
	DarkMinGap      time.Duration // ... and at least this long
	CoverageCellDeg float64       // coverage grid cell size in degrees
	CoverageTTL     time.Duration // coverage rows older than this are pruned

	AbsorbWindow time.Duration // an open finding absorbs evidence this long after its last hit
	Tick         time.Duration // how often the time-based detectors run
	HighSeverity string        // findings at or above this severity become alerts
}

// TypeMap holds a per-entity-type value with a DEFAULT fallback.
type TypeMap map[string]float64

// For returns the value for an entity type, else DEFAULT, else 0.
func (m TypeMap) For(entityType string) float64 {
	if v, ok := m[strings.ToUpper(entityType)]; ok {
		return v
	}
	return m["DEFAULT"]
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		Port:         commonCfg.EnvOrDefault("PORT", "5000"),
		DatabaseURL:  commonCfg.DatabaseURL(),
		KafkaBrokers: commonCfg.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		CORSOrigin:   commonCfg.EnvOrDefault("CORS_ORIGIN", "*"),
		Baseline: BaselineConfig{
			Window:        days("ANALYTICS_BASELINE_WINDOW_DAYS", 30),
			Interval:      minutes("ANALYTICS_BASELINE_INTERVAL_MIN", 60),
			ActiveWindow:  hours("ANALYTICS_BASELINE_ACTIVE_HOURS", 24),
			MinPoints:     commonCfg.EnvOrDefaultInt("ANALYTICS_BASELINE_MIN_POINTS", 200),
			MinDays:       commonCfg.EnvOrDefaultInt("ANALYTICS_BASELINE_MIN_DAYS", 3),
			CacheTTL:      minutes("ANALYTICS_BASELINE_CACHE_TTL_MIN", 10),
			Workers:       commonCfg.EnvOrDefaultInt("ANALYTICS_BASELINE_WORKERS", 4),
			ThinInterval:  seconds("ANALYTICS_BASELINE_THIN_SEC", 30),
			MaxSlowPoints: commonCfg.EnvOrDefaultInt("ANALYTICS_BASELINE_MAX_SLOW_POINTS", 20000),
			DBSCANEpsM:    envFloat("ANALYTICS_DBSCAN_EPS_M", 150),
			DBSCANMinPts:  commonCfg.EnvOrDefaultInt("ANALYTICS_DBSCAN_MIN_PTS", 10),
			SlowKnots:     envTypeMap("ANALYTICS_SLOW_KNOTS", "PERSON=1,VEHICLE=2,VESSEL=1,AIRCRAFT=20,DRONE=2,DEFAULT=2"),
		},
		Detectors: DetectorConfig{
			SkipTypes:        envSet("ANALYTICS_SKIP_TYPES", "SATELLITE,FACILITY,SENSOR,CYBER,SIGNAL"),
			PlaceRangeFactor: envFloat("ANALYTICS_PLACE_RANGE_FACTOR", 1.5),
			PlaceMinM:        envTypeMap("ANALYTICS_PLACE_MIN_M", "PERSON=500,VEHICLE=2000,VESSEL=5000,AIRCRAFT=50000,DRONE=2000,DEFAULT=2000"),
			TimeMinShare:     envFloat("ANALYTICS_TIME_MIN_SHARE", 0.02),
			TimeMinDays:      commonCfg.EnvOrDefaultInt("ANALYTICS_TIME_MIN_DAYS", 14),
			TimeQuietShare:   envFloat("ANALYTICS_TIME_QUIET_SHARE", 0.10),
			TimeQuietGap:     minutes("ANALYTICS_TIME_QUIET_GAP_MIN", 60),
			MoveZ:            envFloat("ANALYTICS_MOVE_Z", 3),
			MoveMaxKnots:     envTypeMap("ANALYTICS_MOVE_MAX_KNOTS", "PERSON=15,VEHICLE=200,VESSEL=60,AIRCRAFT=600,DRONE=100,DEFAULT=100"),
			MoveMinGap:       seconds("ANALYTICS_MOVE_MIN_GAP_SEC", 5),
			DarkGapFactor:    envFloat("ANALYTICS_DARK_GAP_FACTOR", 3),
			DarkMinGap:       minutes("ANALYTICS_DARK_MIN_GAP_MIN", 10),
			CoverageCellDeg:  envFloat("ANALYTICS_COVERAGE_CELL_DEG", 0.05),
			CoverageTTL:      days("ANALYTICS_COVERAGE_TTL_DAYS", 7),
			AbsorbWindow:     minutes("ANALYTICS_FINDING_ABSORB_MIN", 30),
			Tick:             seconds("ANALYTICS_TICK_SEC", 60),
			HighSeverity:     commonCfg.EnvOrDefault("ANALYTICS_ALERT_SEVERITY", "HIGH"),
		},
	}
}

// Thresholds is the detector configuration as it is stamped on a finding,
// so a reviewer sees the values the entity was judged against.
func (c DetectorConfig) Thresholds() map[string]any {
	return map[string]any{
		"placeRangeFactor": c.PlaceRangeFactor,
		"placeMinM":        map[string]float64(c.PlaceMinM),
		"timeMinShare":     c.TimeMinShare,
		"timeMinDays":      c.TimeMinDays,
		"timeQuietShare":   c.TimeQuietShare,
		"timeQuietGapSec":  int(c.TimeQuietGap / time.Second),
		"moveZ":            c.MoveZ,
		"moveMaxKnots":     map[string]float64(c.MoveMaxKnots),
		"darkGapFactor":    c.DarkGapFactor,
		"darkMinGapSec":    int(c.DarkMinGap / time.Second),
		"absorbWindowSec":  int(c.AbsorbWindow / time.Second),
	}
}

func days(key string, def int) time.Duration {
	return time.Duration(commonCfg.EnvOrDefaultInt(key, def)) * 24 * time.Hour
}

func hours(key string, def int) time.Duration {
	return time.Duration(commonCfg.EnvOrDefaultInt(key, def)) * time.Hour
}

func minutes(key string, def int) time.Duration {
	return time.Duration(commonCfg.EnvOrDefaultInt(key, def)) * time.Minute
}

func seconds(key string, def int) time.Duration {
	return time.Duration(commonCfg.EnvOrDefaultInt(key, def)) * time.Second
}

func envFloat(key string, def float64) float64 {
	raw := commonCfg.EnvOrDefault(key, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return def
	}
	return v
}

// envTypeMap parses "TYPE=value,TYPE=value" into a TypeMap; entries that do
// not parse are ignored and the defaults fill the gaps.
func envTypeMap(key, def string) TypeMap {
	out := parseTypeMap(def)
	for k, v := range parseTypeMap(commonCfg.EnvOrDefault(key, "")) {
		out[k] = v
	}
	return out
}

func parseTypeMap(raw string) TypeMap {
	out := TypeMap{}
	for _, part := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
		if err != nil {
			continue
		}
		out[strings.ToUpper(strings.TrimSpace(kv[0]))] = v
	}
	return out
}

func envSet(key, def string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(commonCfg.EnvOrDefault(key, def), ",") {
		if p := strings.ToUpper(strings.TrimSpace(part)); p != "" {
			out[p] = true
		}
	}
	return out
}
