package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

const (
	openSkyBaseURL      = "https://opensky-network.org/api/states/all"
	metersPerSecToKnots = 1.94384
	maxBackoff          = 60 * time.Second

	defaultOpenSkyInterval    = 10 * time.Second
	defaultOpenSkyTTL         = 90 * time.Second
	defaultOpenSkyMinSpacing  = 3 * time.Second
	defaultOpenSkyMaxInterval = 60 * time.Second
	defaultOpenSkyMaxArea     = 400.0
)

// OpenSkyListener polls the OpenSky Network REST API and feeds aircraft
// positions into the ingestion pipeline.
//
// The area it polls is a Viewport. In viewport mode the gateway pushes the
// area connected map clients can see and the listener idles whenever that
// lease is missing, inactive, or older than the TTL. In legacy mode the
// viewport is fixed from OPENSKY_BBOX (empty = global) and never expires.
// Large areas stretch the poll interval (see effectiveInterval) so a global
// view does not burn OpenSky credits at the viewport rate.
type OpenSkyListener struct {
	cfg     *config.Config
	input   chan<- *models.IngestMessage
	logger  *zap.Logger
	metrics *metrics.Metrics
	client  *http.Client
	baseURL string // overridable in tests

	onSuccess func(int, time.Time)
	onError   func()

	mu          sync.Mutex // guards the OAuth token
	accessToken string
	tokenExpiry time.Time

	viewportMode bool
	interval     time.Duration
	ttl          time.Duration
	minSpacing   time.Duration
	maxInterval  time.Duration
	maxArea      float64

	vpMu          sync.RWMutex // guards everything below
	viewport      Viewport
	viewportAt    time.Time // when the viewport was last pushed (lease start)
	lastPollAt    time.Time // last successful poll
	lastAttemptAt time.Time // last poll attempt, success or not
	dirty         bool      // viewport changed since the last attempt
	idle          bool

	wake chan struct{} // cap 1; nudges the poll loop after a viewport change

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewOpenSkyListener creates a new OpenSky polling adapter.
func NewOpenSkyListener(cfg *config.Config, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics, onSuccess func(int, time.Time), onError func()) *OpenSkyListener {
	l := &OpenSkyListener{
		cfg:       cfg,
		input:     input,
		logger:    logger,
		metrics:   m,
		baseURL:   openSkyBaseURL,
		onSuccess: onSuccess,
		onError:   onError,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		viewportMode: cfg.OpenSkyViewportMode,
		interval:     secondsOr(cfg.OpenSkyIntervalSec, defaultOpenSkyInterval),
		ttl:          secondsOr(cfg.OpenSkyViewportTTLSec, defaultOpenSkyTTL),
		minSpacing:   secondsOr(cfg.OpenSkyMinPollSpacingSec, defaultOpenSkyMinSpacing),
		maxInterval:  secondsOr(cfg.OpenSkyMaxIntervalSec, defaultOpenSkyMaxInterval),
		maxArea:      cfg.OpenSkyMaxAreaSqDeg,
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
	}
	if l.maxArea <= 0 {
		l.maxArea = defaultOpenSkyMaxArea
	}

	if !l.viewportMode {
		// Legacy: a fixed area from configuration that never expires.
		l.viewport = Viewport{Active: true}
		if box, err := parseLegacyBBox(cfg.OpenSkyBBox); err != nil {
			logger.Warn("invalid opensky bbox format, using global", zap.String("bbox", cfg.OpenSkyBBox), zap.Error(err))
		} else if box != nil {
			l.viewport.Boxes = []BBox{*box}
		}
	}

	return l
}

func secondsOr(sec int, def time.Duration) time.Duration {
	if sec <= 0 {
		return def
	}
	return time.Duration(sec) * time.Second
}

// parseLegacyBBox parses the OPENSKY_BBOX value "lamin,lomin,lamax,lomax".
// An empty value means global and returns nil, nil.
func parseLegacyBBox(s string) (*BBox, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("expected 4 comma-separated values, got %d", len(parts))
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, fmt.Errorf("value %d: %w", i, err)
		}
		vals[i] = f
	}
	box := BBox{South: vals[0], West: vals[1], North: vals[2], East: vals[3]}
	if err := box.Validate(); err != nil {
		return nil, err
	}
	return &box, nil
}

// Start launches the background polling goroutine. It returns immediately.
func (l *OpenSkyListener) Start() error {
	l.logger.Info("starting opensky listener",
		zap.Duration("interval", l.interval),
		zap.Bool("viewport_mode", l.viewportMode),
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

// SetViewport replaces the polled area. A change wakes the poll loop so the
// new area is fetched after at most the minimum spacing. It also refreshes
// the lease, so unchanged pushes act as a heartbeat.
func (l *OpenSkyListener) SetViewport(v Viewport) ViewportStatus {
	l.vpMu.Lock()
	changed := !l.viewport.Equal(v)
	l.viewport = v
	l.viewportAt = time.Now()
	if changed {
		l.dirty = true
	}
	status := l.statusLocked()
	l.vpMu.Unlock()

	if changed {
		l.logger.Info("opensky viewport updated",
			zap.Bool("active", v.Active),
			zap.Int("clients", v.Clients),
			zap.Int("boxes", len(v.Boxes)),
			zap.Float64("area_sq_deg", v.Area()),
			zap.Duration("effective_interval", status.EffectiveInterval),
		)
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
	return status
}

// ViewportStatus reports the current area and poll timing.
func (l *OpenSkyListener) ViewportStatus() ViewportStatus {
	l.vpMu.RLock()
	defer l.vpMu.RUnlock()
	return l.statusLocked()
}

// statusLocked must be called with vpMu held (read or write).
func (l *OpenSkyListener) statusLocked() ViewportStatus {
	vp := l.viewport
	st := ViewportStatus{
		Active:            vp.Active,
		Clients:           vp.Clients,
		Boxes:             append([]BBox(nil), vp.Boxes...),
		AreaSqDeg:         vp.Area(),
		EffectiveInterval: effectiveInterval(l.interval, l.maxInterval, vp.Area(), l.maxArea),
		LastPollAt:        l.lastPollAt,
		Idle:              l.idleLocked(),
	}
	if l.viewportMode && !l.viewportAt.IsZero() {
		st.ExpiresAt = l.viewportAt.Add(l.ttl)
	}
	if !st.Idle {
		next := l.nextAttemptLocked(0)
		if wait := time.Until(next); wait > 0 {
			st.NextPollIn = wait
		}
	}
	return st
}

// idleLocked reports whether the loop should sleep until a viewport arrives.
func (l *OpenSkyListener) idleLocked() bool {
	if !l.viewportMode {
		return false
	}
	return !l.viewport.Active || len(l.viewport.Boxes) == 0 || time.Since(l.viewportAt) > l.ttl
}

// nextAttemptLocked returns when the next poll is due. A viewport change
// only has to respect the minimum spacing; otherwise the stretched interval
// plus any error backoff applies. Before the first attempt, polling is due
// immediately.
func (l *OpenSkyListener) nextAttemptLocked(backoff time.Duration) time.Time {
	if l.lastAttemptAt.IsZero() {
		return time.Now()
	}
	wait := effectiveInterval(l.interval, l.maxInterval, l.viewport.Area(), l.maxArea)
	if l.dirty {
		wait = l.minSpacing
	}
	return l.lastAttemptAt.Add(wait + backoff)
}

// effectiveInterval stretches the base interval for large areas: up to
// maxArea the base applies, beyond it the interval grows with ceil(area /
// maxArea), capped at maxInterval.
func effectiveInterval(base, maxInterval time.Duration, area, maxArea float64) time.Duration {
	if maxArea <= 0 || area <= maxArea {
		return base
	}
	factor := math.Ceil(area / maxArea)
	stretched := time.Duration(float64(base) * factor)
	if stretched > maxInterval {
		return maxInterval
	}
	return stretched
}

// pollLoop schedules polls from the current viewport. It is a timer loop
// rather than a ticker so a viewport change can pull the next poll forward
// and an idle period costs nothing.
func (l *OpenSkyListener) pollLoop() {
	defer l.wg.Done()

	backoff := time.Duration(0)
	idleLogged := false

	for {
		l.vpMu.Lock()
		idle := l.idleLocked()
		l.idle = idle
		next := l.nextAttemptLocked(backoff)
		l.vpMu.Unlock()

		if idle {
			if !idleLogged {
				l.logger.Info("opensky idle: waiting for an active viewport")
				idleLogged = true
			}
			select {
			case <-l.stop:
				return
			case <-l.wake:
				continue
			}
		}
		idleLogged = false

		timer := time.NewTimer(time.Until(next))
		select {
		case <-l.stop:
			timer.Stop()
			return
		case <-l.wake:
			timer.Stop()
			continue // recompute the schedule for the new viewport
		case <-timer.C:
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

// poll fetches the current state vectors for every box of the viewport and
// sends each aircraft position to the pipeline input channel.
func (l *OpenSkyListener) poll() error {
	l.vpMu.Lock()
	boxes := append([]BBox(nil), l.viewport.Boxes...)
	l.lastAttemptAt = time.Now()
	l.dirty = false
	l.vpMu.Unlock()

	var states []stateVector
	if len(boxes) == 0 {
		var err error
		if states, err = l.pollBox(nil); err != nil {
			return err
		}
	} else {
		seen := make(map[string]struct{})
		for i := range boxes {
			box := boxes[i]
			part, err := l.pollBox(&box)
			if err != nil {
				return err
			}
			for _, sv := range part {
				if _, dup := seen[sv.Icao24]; dup {
					continue
				}
				seen[sv.Icao24] = struct{}{}
				states = append(states, sv)
			}
		}
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
	l.vpMu.Lock()
	l.lastPollAt = now
	l.vpMu.Unlock()

	l.metrics.FeedLastSuccess.WithLabelValues("opensky").Set(float64(now.Unix()))
	l.metrics.FeedEntityCount.WithLabelValues("opensky").Set(float64(sent))
	if l.onSuccess != nil {
		l.onSuccess(sent, now)
	}

	l.logger.Info("opensky poll complete",
		zap.Int("aircraft", sent),
		zap.Int("total_states", len(states)),
		zap.Int("boxes", len(boxes)),
	)

	return nil
}

// pollBox performs one OpenSky request for a box (nil = global) and parses
// the state vectors.
func (l *OpenSkyListener) pollBox(box *BBox) ([]stateVector, error) {
	req, err := http.NewRequest(http.MethodGet, l.buildURL(box), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if l.cfg.OpenSkyClientID != "" && l.cfg.OpenSkyClientSecret != "" {
		token, err := l.getAccessToken()
		if err != nil {
			return nil, fmt.Errorf("oauth2 token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if remaining := resp.Header.Get("X-Rate-Limit-Remaining"); remaining != "" {
		l.logger.Debug("opensky credits remaining", zap.String("remaining", remaining))
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("opensky api returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	states, err := parseOpenSkyResponse(body)
	if err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return states, nil
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

// buildURL constructs the OpenSky API URL for a box; nil or a global box
// means no bounding-box parameters.
func (l *OpenSkyListener) buildURL(box *BBox) string {
	if box == nil || box.IsGlobal() {
		return l.baseURL
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	return fmt.Sprintf("%s?lamin=%s&lomin=%s&lamax=%s&lomax=%s",
		l.baseURL, f(box.South), f(box.West), f(box.North), f(box.East))
}

// openSkyResponse is the top-level JSON response from the OpenSky API.
type openSkyResponse struct {
	Time   int64           `json:"time"`
	States [][]interface{} `json:"states"`
}

// stateVector holds the parsed fields from a single OpenSky state vector.
type stateVector struct {
	Icao24        string
	Callsign      string
	OriginCountry string
	Longitude     *float64
	Latitude      *float64
	BaroAltitude  *float64
	GeoAltitude   *float64
	OnGround      bool
	Velocity      *float64
	TrueTrack     *float64
	VerticalRate  *float64
	TimePosition  *int64
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
			Icao24:        stringAt(raw, 0),
			Callsign:      strings.TrimSpace(stringAt(raw, 1)),
			OriginCountry: stringAt(raw, 2),
			Longitude:     floatAt(raw, 5),
			Latitude:      floatAt(raw, 6),
			BaroAltitude:  floatAt(raw, 7),
			OnGround:      boolAt(raw, 8),
			Velocity:      floatAt(raw, 9),
			TrueTrack:     floatAt(raw, 10),
			VerticalRate:  floatAt(raw, 11),
			GeoAltitude:   floatAt(raw, 13),
			TimePosition:  intAt(raw, 3),
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
		EntityID:         "ICAO-" + strings.ToUpper(sv.Icao24),
		EntityType:       models.EntityTypeAircraft,
		Source:           models.SourceOpenSky,
		Latitude:         *sv.Latitude,
		Longitude:        *sv.Longitude,
		TrackEnvironment: "AIR",
	}

	// Name: callsign if available, else ICAO hex.
	if sv.Callsign != "" {
		ep.Name = sv.Callsign
	} else {
		ep.Name = sv.Icao24
	}

	// Altitude: prefer barometric, fall back to geometric.
	adsbData := &models.ADSBData{
		ICAOHex:  strings.ToUpper(sv.Icao24),
		OnGround: sv.OnGround,
	}
	if sv.BaroAltitude != nil {
		ep.Altitude = *sv.BaroAltitude
		adsbData.AltitudeBaro = *sv.BaroAltitude
	} else if sv.GeoAltitude != nil {
		ep.Altitude = *sv.GeoAltitude
	}
	if sv.GeoAltitude != nil {
		adsbData.AltitudeGeom = *sv.GeoAltitude
	}

	// Velocity: m/s to knots. Also decompose into NEU components.
	if sv.Velocity != nil {
		ep.SpeedKnots = *sv.Velocity * metersPerSecToKnots
		adsbData.GroundSpeed = *sv.Velocity * metersPerSecToKnots

		// Decompose ground velocity into North-East components using track angle.
		if sv.TrueTrack != nil {
			trackRad := *sv.TrueTrack * math.Pi / 180.0
			ep.VelocityNorth = *sv.Velocity * math.Cos(trackRad)
			ep.VelocityEast = *sv.Velocity * math.Sin(trackRad)
		}
	}

	// Vertical rate (m/s, positive = climbing)
	if sv.VerticalRate != nil {
		ep.VelocityUp = *sv.VerticalRate
		adsbData.VerticalRate = *sv.VerticalRate
	}

	// Heading and course from true_track.
	if sv.TrueTrack != nil {
		ep.Heading = *sv.TrueTrack
		ep.Course = *sv.TrueTrack
	}

	// Country of origin
	if sv.OriginCountry != "" {
		ep.CountryOfOrigin = sv.OriginCountry
	}

	// Callsign as aircraft ID
	if sv.Callsign != "" {
		adsbData.AircraftID = sv.Callsign
	}

	ep.ADSBData = adsbData

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
