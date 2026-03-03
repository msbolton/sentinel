package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

const (
	defaultADSBLolEndpoint = "https://api.adsb.lol/v2/mil"
	adsbLolMaxBackoff      = 60 * time.Second
)

// ADSBLolListener polls the adsb.lol military aircraft REST API at a
// configurable interval and feeds aircraft positions into the ingestion pipeline.
type ADSBLolListener struct {
	cfg     *config.Config
	input   chan<- *models.IngestMessage
	logger  *zap.Logger
	metrics *metrics.Metrics
	client  *http.Client

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewADSBLolListener creates a new adsb.lol polling adapter.
func NewADSBLolListener(cfg *config.Config, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics) *ADSBLolListener {
	return &ADSBLolListener{
		cfg:     cfg,
		input:   input,
		logger:  logger,
		metrics: m,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		stop: make(chan struct{}),
	}
}

// Start launches the background polling goroutine. It returns immediately.
func (l *ADSBLolListener) Start() error {
	l.logger.Info("starting adsblol listener",
		zap.Int("interval_sec", l.cfg.ADSBLolIntervalSec),
		zap.String("endpoint", l.cfg.ADSBLolEndpoint),
	)

	l.metrics.ActiveConnections.WithLabelValues(models.SourceADSBLol).Inc()

	l.wg.Add(1)
	go l.pollLoop()

	return nil
}

// Stop signals the polling goroutine to exit and waits for it to finish.
func (l *ADSBLolListener) Stop() {
	l.stopOnce.Do(func() {
		l.logger.Info("stopping adsblol listener")
		close(l.stop)
		l.wg.Wait()
		l.metrics.ActiveConnections.WithLabelValues(models.SourceADSBLol).Dec()
		l.logger.Info("adsblol listener stopped")
	})
}

// pollLoop runs the poll ticker with exponential backoff on errors.
func (l *ADSBLolListener) pollLoop() {
	defer l.wg.Done()

	interval := time.Duration(l.cfg.ADSBLolIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	backoff := time.Duration(0)

	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			if backoff > 0 {
				l.logger.Debug("adsblol backoff", zap.Duration("delay", backoff))
				select {
				case <-l.stop:
					return
				case <-time.After(backoff):
				}
			}

			if err := l.poll(); err != nil {
				l.logger.Warn("adsblol poll failed", zap.Error(err))
				l.metrics.MessagesFailed.WithLabelValues(models.SourceADSBLol, "poll_error").Inc()

				// Exponential backoff: 1s, 2s, 4s, ... capped at 60s.
				if backoff == 0 {
					backoff = 1 * time.Second
				} else {
					backoff *= 2
					if backoff > adsbLolMaxBackoff {
						backoff = adsbLolMaxBackoff
					}
				}
			} else {
				backoff = 0
			}
		}
	}
}

// poll fetches the current military aircraft from the adsb.lol API and sends
// each aircraft position to the pipeline input channel.
func (l *ADSBLolListener) poll() error {
	endpoint := l.cfg.ADSBLolEndpoint
	if endpoint == "" {
		endpoint = defaultADSBLolEndpoint
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("adsb.lol api returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	aircraft, err := parseADSBLolResponse(body)
	if err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	sent := 0
	for _, ac := range aircraft {
		ep := adsbLolAircraftToEntityPosition(ac)
		if ep == nil {
			continue // skip aircraft with no position
		}

		payload, err := json.Marshal(ep)
		if err != nil {
			l.logger.Warn("failed to marshal entity position", zap.Error(err))
			continue
		}

		msg := &models.IngestMessage{
			SourceType: models.SourceADSBLol,
			SourceAddr: "api.adsb.lol",
			Payload:    payload,
			ReceivedAt: time.Now().UTC(),
		}

		select {
		case l.input <- msg:
			sent++
		default:
			l.logger.Warn("pipeline input full, dropping adsblol message",
				zap.String("entity_id", ep.EntityID),
			)
			l.metrics.MessagesFailed.WithLabelValues(models.SourceADSBLol, "queue_full").Inc()
		}
	}

	l.metrics.MessagesReceived.WithLabelValues(models.SourceADSBLol).Add(float64(sent))
	l.logger.Debug("adsblol poll complete", zap.Int("aircraft", sent))

	return nil
}

// adsbLolResponse is the top-level JSON response from the adsb.lol API.
type adsbLolResponse struct {
	AC    []adsbLolAircraft `json:"ac"`
	Msg   string            `json:"msg"`
	Now   int64             `json:"now"`
	Total int               `json:"total"`
}

// adsbLolAircraft holds the parsed fields from a single aircraft in the response.
type adsbLolAircraft struct {
	Hex     string   `json:"hex"`
	Flight  *string  `json:"flight"`    // nullable
	Lat     *float64 `json:"lat"`       // nullable
	Lon     *float64 `json:"lon"`       // nullable
	AltBaro *float64 `json:"alt_baro"`  // feet, nullable
	GS      *float64 `json:"gs"`        // knots, nullable
	Track   *float64 `json:"track"`     // degrees, nullable
	Type    string   `json:"type"`      // adsb_icao, mlat, etc.
	Reg     *string  `json:"r"`         // registration
	TCode   *string  `json:"t"`         // aircraft type (C17A, etc.)
}

// parseADSBLolResponse unmarshals the JSON body and extracts aircraft.
func parseADSBLolResponse(body []byte) ([]adsbLolAircraft, error) {
	var resp adsbLolResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if resp.AC == nil {
		return nil, nil
	}

	return resp.AC, nil
}

// adsbLolAircraftToEntityPosition converts a parsed aircraft to the canonical
// EntityPosition format. Returns nil if the aircraft has no valid position.
func adsbLolAircraftToEntityPosition(ac adsbLolAircraft) *models.EntityPosition {
	// Require position
	if ac.Lat == nil || ac.Lon == nil {
		return nil
	}

	ep := &models.EntityPosition{
		EntityID:   "ICAO-" + strings.ToUpper(ac.Hex),
		EntityType: models.EntityTypeAircraft,
		Source:     models.SourceADSBLol,
		Latitude:   *ac.Lat,
		Longitude:  *ac.Lon,
		Timestamp:  time.Now().UTC(),
	}

	// Name: flight callsign (trimmed), fallback to hex
	if ac.Flight != nil && strings.TrimSpace(*ac.Flight) != "" {
		ep.Name = strings.TrimSpace(*ac.Flight)
	} else {
		ep.Name = ac.Hex
	}

	// Altitude (already in feet)
	if ac.AltBaro != nil {
		ep.Altitude = *ac.AltBaro
	}

	// Speed (already in knots - no conversion needed!)
	if ac.GS != nil {
		ep.SpeedKnots = *ac.GS
	}

	// Heading/course from track
	if ac.Track != nil {
		ep.Heading = *ac.Track
		ep.Course = *ac.Track
	}

	return ep
}
