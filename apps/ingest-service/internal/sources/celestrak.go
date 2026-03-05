package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	satellite "github.com/joshuaferrara/go-satellite"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

const celestrakBaseURL = "https://celestrak.org/NORAD/elements/gp.php"

// kmsToKnots converts km/s to knots.
const kmsToKnots = 1943.84

// CelesTrakListener fetches TLE data from CelesTrak, propagates satellite
// positions using SGP4, and feeds them into the ingestion pipeline.
type CelesTrakListener struct {
	cfg     *config.Config
	input   chan<- *models.IngestMessage
	logger  *zap.Logger
	metrics *metrics.Metrics
	client  *http.Client

	tleCache map[int]cachedTLE // NORAD ID → parsed TLE
	tleMu    sync.RWMutex

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// cachedTLE holds a parsed TLE entry ready for SGP4 propagation.
type cachedTLE struct {
	Name    string
	Line1   string
	Line2   string
	NoradID int
	Sat     satellite.Satellite
}

// rawTLE holds the raw text of a single TLE entry before parsing.
type rawTLE struct {
	Name    string
	Line1   string
	Line2   string
	NoradID int
}

// NewCelesTrakListener creates a new CelesTrak satellite adapter.
func NewCelesTrakListener(cfg *config.Config, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics) *CelesTrakListener {
	return &CelesTrakListener{
		cfg:      cfg,
		input:    input,
		logger:   logger,
		metrics:  m,
		client:   &http.Client{Timeout: 60 * time.Second},
		tleCache: make(map[int]cachedTLE),
		stop:     make(chan struct{}),
	}
}

// Start launches the TLE refresh and position propagation goroutines.
func (l *CelesTrakListener) Start() error {
	l.logger.Info("starting celestrak listener",
		zap.String("groups", l.cfg.CelesTrakGroups),
		zap.Int("tle_refresh_hours", l.cfg.CelesTrakTLERefreshHours),
		zap.Int("propagation_interval_sec", l.cfg.CelesTrakPropagationIntervalSec),
	)

	l.metrics.ActiveConnections.WithLabelValues(models.SourceCelesTrak).Inc()

	// Synchronous initial TLE fetch so the first propagation has data.
	l.refreshTLEs()

	l.wg.Add(2)
	go l.tleRefreshLoop()
	go l.propagationLoop()

	return nil
}

// Stop signals both goroutines to exit and waits for them to finish.
func (l *CelesTrakListener) Stop() {
	l.stopOnce.Do(func() {
		l.logger.Info("stopping celestrak listener")
		close(l.stop)
		l.wg.Wait()
		l.metrics.ActiveConnections.WithLabelValues(models.SourceCelesTrak).Dec()
		l.logger.Info("celestrak listener stopped")
	})
}

// tleRefreshLoop periodically re-fetches TLE data from CelesTrak.
func (l *CelesTrakListener) tleRefreshLoop() {
	defer l.wg.Done()

	interval := time.Duration(l.cfg.CelesTrakTLERefreshHours) * time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.refreshTLEs()
		}
	}
}

// refreshTLEs fetches TLEs for all configured groups and updates the cache.
func (l *CelesTrakListener) refreshTLEs() {
	groups := strings.Split(l.cfg.CelesTrakGroups, ",")
	backoff := time.Duration(0)

	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}

		// Check for stop signal before fetching.
		select {
		case <-l.stop:
			return
		default:
		}

		if backoff > 0 {
			l.logger.Debug("celestrak backoff", zap.Duration("delay", backoff))
			select {
			case <-l.stop:
				return
			case <-time.After(backoff):
			}
		}

		tles, err := l.fetchTLEs(group)
		if err != nil {
			l.logger.Warn("celestrak TLE fetch failed",
				zap.String("group", group),
				zap.Error(err),
			)
			l.metrics.MessagesFailed.WithLabelValues(models.SourceCelesTrak, "tle_fetch_error").Inc()

			if backoff == 0 {
				backoff = 1 * time.Second
			} else {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = 0

		parsed := 0
		l.tleMu.Lock()
		for _, raw := range tles {
			sat := satellite.TLEToSat(raw.Line1, raw.Line2, satellite.GravityWGS84)
			l.tleCache[raw.NoradID] = cachedTLE{
				Name:    raw.Name,
				Line1:   raw.Line1,
				Line2:   raw.Line2,
				NoradID: raw.NoradID,
				Sat:     sat,
			}
			parsed++
		}
		l.tleMu.Unlock()

		l.logger.Info("celestrak TLE refresh complete",
			zap.String("group", group),
			zap.Int("satellites", parsed),
		)
	}
}

// fetchTLEs downloads TLE data for a single CelesTrak group.
func (l *CelesTrakListener) fetchTLEs(group string) ([]rawTLE, error) {
	url := fmt.Sprintf("%s?GROUP=%s&FORMAT=tle", celestrakBaseURL, group)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("celestrak api returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseTLEText(string(body))
}

// parseTLEText parses CelesTrak TLE text format into rawTLE entries.
// Each entry is 3 lines: name, line1 (starts with "1 "), line2 (starts with "2 ").
func parseTLEText(body string) ([]rawTLE, error) {
	lines := strings.Split(strings.TrimSpace(body), "\n")

	var result []rawTLE
	for i := 0; i+2 < len(lines); i += 3 {
		name := strings.TrimSpace(lines[i])
		line1 := strings.TrimSpace(lines[i+1])
		line2 := strings.TrimSpace(lines[i+2])

		if !strings.HasPrefix(line1, "1 ") || !strings.HasPrefix(line2, "2 ") {
			continue
		}

		// NORAD catalog ID is at line1[2:7].
		if len(line1) < 7 {
			continue
		}
		noradStr := strings.TrimSpace(line1[2:7])
		noradID, err := strconv.Atoi(noradStr)
		if err != nil {
			continue
		}

		result = append(result, rawTLE{
			Name:    name,
			Line1:   line1,
			Line2:   line2,
			NoradID: noradID,
		})
	}

	return result, nil
}

// propagationLoop periodically propagates all cached TLEs to current positions.
func (l *CelesTrakListener) propagationLoop() {
	defer l.wg.Done()

	interval := time.Duration(l.cfg.CelesTrakPropagationIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.propagateAll()
		}
	}
}

// propagateAll propagates all cached TLEs to the current time and sends
// the resulting positions to the pipeline.
func (l *CelesTrakListener) propagateAll() {
	now := time.Now().UTC()

	l.tleMu.RLock()
	snapshot := make([]cachedTLE, 0, len(l.tleCache))
	for _, tle := range l.tleCache {
		snapshot = append(snapshot, tle)
	}
	l.tleMu.RUnlock()

	if len(snapshot) == 0 {
		return
	}

	sent := 0
	for _, tle := range snapshot {
		ep := propagateToEntityPosition(tle, now)
		if ep == nil {
			continue
		}

		payload, err := json.Marshal(ep)
		if err != nil {
			l.logger.Warn("failed to marshal satellite position", zap.Error(err))
			continue
		}

		msg := &models.IngestMessage{
			SourceType: models.SourceCelesTrak,
			SourceAddr: "celestrak.org",
			Payload:    payload,
			ReceivedAt: now,
		}

		select {
		case l.input <- msg:
			sent++
		default:
			l.logger.Warn("pipeline input full, dropping celestrak message",
				zap.String("entity_id", ep.EntityID),
			)
			l.metrics.MessagesFailed.WithLabelValues(models.SourceCelesTrak, "queue_full").Inc()
		}
	}

	l.metrics.MessagesReceived.WithLabelValues(models.SourceCelesTrak).Add(float64(sent))
	l.logger.Info("celestrak propagation complete",
		zap.Int("satellites", sent),
		zap.Int("total_cached", len(snapshot)),
	)
}

// propagateToEntityPosition uses SGP4 to compute the current position
// of a satellite from its TLE. Returns nil if the propagation produces
// invalid results (e.g. decayed orbit with NaN/Inf coordinates).
func propagateToEntityPosition(tle cachedTLE, now time.Time) *models.EntityPosition {
	year, month, day := now.Date()
	hour, min, sec := now.Clock()

	pos, vel := satellite.Propagate(tle.Sat, year, int(month), day, hour, min, sec)

	// Check for NaN/Inf which indicates a decayed orbit or bad TLE.
	if math.IsNaN(pos.X) || math.IsNaN(pos.Y) || math.IsNaN(pos.Z) ||
		math.IsInf(pos.X, 0) || math.IsInf(pos.Y, 0) || math.IsInf(pos.Z, 0) {
		return nil
	}

	gmst := satellite.GSTimeFromDate(year, int(month), day, hour, min, sec)
	alt, _, latLng := satellite.ECIToLLA(pos, gmst)
	latLngDeg := satellite.LatLongDeg(latLng)

	// Validate coordinates.
	if math.IsNaN(latLngDeg.Latitude) || math.IsNaN(latLngDeg.Longitude) || math.IsNaN(alt) {
		return nil
	}

	// Compute speed from velocity vector (km/s → knots).
	speedKms := math.Sqrt(vel.X*vel.X + vel.Y*vel.Y + vel.Z*vel.Z)
	speedKnots := speedKms * kmsToKnots

	return &models.EntityPosition{
		EntityID:   fmt.Sprintf("SAT-%d", tle.NoradID),
		EntityType: models.EntityTypeSatellite,
		Name:       tle.Name,
		Source:     models.SourceCelesTrak,
		Latitude:   latLngDeg.Latitude,
		Longitude:  latLngDeg.Longitude,
		Altitude:   alt * 1000, // km → meters for consistency with other sources
		SpeedKnots: speedKnots,
		Timestamp:  now,
	}
}
