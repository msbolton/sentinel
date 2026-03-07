package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

const (
	openSkyBaseURL       = "https://opensky-network.org/api/states/all"
	metersPerSecToKnots  = 1.94384
	maxBackoff           = 60 * time.Second
)

// OpenSkyListener polls the OpenSky Network REST API at a configurable
// interval and feeds aircraft positions into the ingestion pipeline.
type OpenSkyListener struct {
	cfg     *config.Config
	input   chan<- *models.IngestMessage
	logger  *zap.Logger
	metrics *metrics.Metrics
	client  *http.Client

	onSuccess func(int, time.Time)
	onError   func()

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewOpenSkyListener creates a new OpenSky polling adapter.
func NewOpenSkyListener(cfg *config.Config, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics, onSuccess func(int, time.Time), onError func()) *OpenSkyListener {
	return &OpenSkyListener{
		cfg:       cfg,
		input:     input,
		logger:    logger,
		metrics:   m,
		onSuccess: onSuccess,
		onError:   onError,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		stop: make(chan struct{}),
	}
}

// Start launches the background polling goroutine. It returns immediately.
func (l *OpenSkyListener) Start() error {
	l.logger.Info("starting opensky listener",
		zap.Int("interval_sec", l.cfg.OpenSkyIntervalSec),
		zap.String("bbox", l.cfg.OpenSkyBBox),
		zap.Bool("authenticated", l.cfg.OpenSkyClientID != ""),
	)

	l.metrics.ActiveConnections.WithLabelValues(models.SourceOpenSky).Inc()

	l.wg.Add(1)
	go l.pollLoop()

	return nil
}

// Stop signals the polling goroutine to exit and waits for it to finish.
func (l *OpenSkyListener) Stop() {
	l.stopOnce.Do(func() {
		l.logger.Info("stopping opensky listener")
		close(l.stop)
		l.wg.Wait()
		l.metrics.ActiveConnections.WithLabelValues(models.SourceOpenSky).Dec()
		l.logger.Info("opensky listener stopped")
	})
}

// pollLoop runs the poll ticker with exponential backoff on errors.
func (l *OpenSkyListener) pollLoop() {
	defer l.wg.Done()

	interval := time.Duration(l.cfg.OpenSkyIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	backoff := time.Duration(0)

	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			if backoff > 0 {
				l.logger.Debug("opensky backoff", zap.Duration("delay", backoff))
				select {
				case <-l.stop:
					return
				case <-time.After(backoff):
				}
			}

			if err := l.poll(); err != nil {
				l.logger.Warn("opensky poll failed", zap.Error(err))
				l.metrics.MessagesFailed.WithLabelValues(models.SourceOpenSky, "poll_error").Inc()
				l.metrics.FeedErrorsTotal.WithLabelValues("opensky").Inc()
				if l.onError != nil {
					l.onError()
				}

				// Exponential backoff: 1s, 2s, 4s, ... capped at 60s.
				if backoff == 0 {
					backoff = 1 * time.Second
				} else {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
			} else {
				backoff = 0
			}
		}
	}
}

// poll fetches the current state vectors from the OpenSky API and sends
// each aircraft position to the pipeline input channel.
func (l *OpenSkyListener) poll() error {
	url := l.buildURL()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if l.cfg.OpenSkyClientID != "" && l.cfg.OpenSkyClientSecret != "" {
		token, err := l.getAccessToken()
		if err != nil {
			return fmt.Errorf("oauth2 token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("opensky api returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	states, err := parseOpenSkyResponse(body)
	if err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	sent := 0
	for _, sv := range states {
		ep := stateVectorToEntityPosition(sv)
		if ep == nil {
			continue // skip aircraft with no position
		}

		payload, err := json.Marshal(ep)
		if err != nil {
			l.logger.Warn("failed to marshal entity position", zap.Error(err))
			continue
		}

		msg := &models.IngestMessage{
			SourceType: models.SourceOpenSky,
			SourceAddr: "opensky-network.org",
			Payload:    payload,
			ReceivedAt: time.Now().UTC(),
		}

		select {
		case l.input <- msg:
			sent++
		default:
			l.logger.Warn("pipeline input full, dropping opensky message",
				zap.String("entity_id", ep.EntityID),
			)
			l.metrics.MessagesFailed.WithLabelValues(models.SourceOpenSky, "queue_full").Inc()
		}
	}

	l.metrics.MessagesReceived.WithLabelValues(models.SourceOpenSky).Add(float64(sent))

	now := time.Now()
	l.metrics.FeedLastSuccess.WithLabelValues("opensky").Set(float64(now.Unix()))
	l.metrics.FeedEntityCount.WithLabelValues("opensky").Set(float64(sent))
	if l.onSuccess != nil {
		l.onSuccess(sent, now)
	}

	l.logger.Info("opensky poll complete", zap.Int("aircraft", sent), zap.Int("total_states", len(states)))

	return nil
}

// getAccessToken returns a cached OAuth2 access token, refreshing it if expired.
func (l *OpenSkyListener) getAccessToken() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Return cached token if still valid (with 30s buffer)
	if l.accessToken != "" && time.Now().Before(l.tokenExpiry.Add(-30*time.Second)) {
		return l.accessToken, nil
	}

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {l.cfg.OpenSkyClientID},
		"client_secret": {l.cfg.OpenSkyClientSecret},
	}

	resp, err := l.client.PostForm(l.cfg.OpenSkyTokenURL, data)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	l.accessToken = tokenResp.AccessToken
	l.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	l.logger.Info("opensky oauth2 token refreshed",
		zap.Int("expires_in_sec", tokenResp.ExpiresIn),
	)

	return l.accessToken, nil
}

// buildURL constructs the OpenSky API URL with optional bounding-box params.
func (l *OpenSkyListener) buildURL() string {
	if l.cfg.OpenSkyBBox == "" {
		return openSkyBaseURL
	}

	// Expected format: "lamin,lomin,lamax,lomax"
	parts := strings.Split(l.cfg.OpenSkyBBox, ",")
	if len(parts) != 4 {
		l.logger.Warn("invalid opensky bbox format, using global", zap.String("bbox", l.cfg.OpenSkyBBox))
		return openSkyBaseURL
	}

	return fmt.Sprintf("%s?lamin=%s&lomin=%s&lamax=%s&lomax=%s",
		openSkyBaseURL,
		strings.TrimSpace(parts[0]),
		strings.TrimSpace(parts[1]),
		strings.TrimSpace(parts[2]),
		strings.TrimSpace(parts[3]),
	)
}

// openSkyResponse is the top-level JSON response from the OpenSky API.
type openSkyResponse struct {
	Time   int64           `json:"time"`
	States [][]interface{} `json:"states"`
}

// stateVector holds the parsed fields from a single OpenSky state vector.
type stateVector struct {
	Icao24       string
	Callsign     string
	Longitude    *float64
	Latitude     *float64
	BaroAltitude *float64
	GeoAltitude  *float64
	OnGround     bool
	Velocity     *float64
	TrueTrack    *float64
	TimePosition *int64
}

// parseOpenSkyResponse unmarshals the JSON body and extracts state vectors.
func parseOpenSkyResponse(body []byte) ([]stateVector, error) {
	var resp openSkyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if resp.States == nil {
		return nil, nil
	}

	vectors := make([]stateVector, 0, len(resp.States))
	for _, raw := range resp.States {
		if len(raw) < 17 {
			continue
		}

		sv := stateVector{
			Icao24:       stringAt(raw, 0),
			Callsign:     strings.TrimSpace(stringAt(raw, 1)),
			Longitude:    floatAt(raw, 5),
			Latitude:     floatAt(raw, 6),
			BaroAltitude: floatAt(raw, 7),
			OnGround:     boolAt(raw, 8),
			Velocity:     floatAt(raw, 9),
			TrueTrack:    floatAt(raw, 10),
			GeoAltitude:  floatAt(raw, 13),
			TimePosition: intAt(raw, 3),
		}

		vectors = append(vectors, sv)
	}

	return vectors, nil
}

// stateVectorToEntityPosition converts a parsed state vector to the canonical
// EntityPosition format. Returns nil if the aircraft has no valid position.
func stateVectorToEntityPosition(sv stateVector) *models.EntityPosition {
	if sv.Latitude == nil || sv.Longitude == nil {
		return nil
	}

	ep := &models.EntityPosition{
		EntityID:   "ICAO-" + strings.ToUpper(sv.Icao24),
		EntityType: models.EntityTypeAircraft,
		Source:     models.SourceOpenSky,
		Latitude:   *sv.Latitude,
		Longitude:  *sv.Longitude,
	}

	// Name: callsign if available, else ICAO hex.
	if sv.Callsign != "" {
		ep.Name = sv.Callsign
	} else {
		ep.Name = sv.Icao24
	}

	// Altitude: prefer barometric, fall back to geometric.
	if sv.BaroAltitude != nil {
		ep.Altitude = *sv.BaroAltitude
	} else if sv.GeoAltitude != nil {
		ep.Altitude = *sv.GeoAltitude
	}

	// Velocity: m/s to knots.
	if sv.Velocity != nil {
		ep.SpeedKnots = *sv.Velocity * metersPerSecToKnots
	}

	// Heading and course from true_track.
	if sv.TrueTrack != nil {
		ep.Heading = *sv.TrueTrack
		ep.Course = *sv.TrueTrack
	}

	// Timestamp from time_position (Unix seconds).
	if sv.TimePosition != nil {
		ep.Timestamp = time.Unix(*sv.TimePosition, 0).UTC()
	} else {
		ep.Timestamp = time.Now().UTC()
	}

	return ep
}

// -- Nil-safe helpers for extracting values from []interface{} arrays. --------

func stringAt(arr []interface{}, idx int) string {
	if idx >= len(arr) || arr[idx] == nil {
		return ""
	}
	s, ok := arr[idx].(string)
	if !ok {
		return ""
	}
	return s
}

func floatAt(arr []interface{}, idx int) *float64 {
	if idx >= len(arr) || arr[idx] == nil {
		return nil
	}
	switch v := arr[idx].(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return &v
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil
		}
		return &f
	default:
		return nil
	}
}

func boolAt(arr []interface{}, idx int) bool {
	if idx >= len(arr) || arr[idx] == nil {
		return false
	}
	b, ok := arr[idx].(bool)
	if !ok {
		return false
	}
	return b
}

func intAt(arr []interface{}, idx int) *int64 {
	if idx >= len(arr) || arr[idx] == nil {
		return nil
	}
	switch v := arr[idx].(type) {
	case float64:
		i := int64(v)
		return &i
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return nil
		}
		return &i
	default:
		return nil
	}
}
