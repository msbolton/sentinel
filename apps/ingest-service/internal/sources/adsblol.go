package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

const adsbLolBaseURL = "https://api.adsb.lol/v2/mil"

// ADSBLolListener polls the adsb.lol military aircraft API at a configurable
// interval and feeds aircraft positions into the ingestion pipeline.
type ADSBLolListener struct {
	cfg     *config.Config
	input   chan<- *models.IngestMessage
	logger  *zap.Logger
	metrics *metrics.Metrics
	client  *http.Client

	onSuccess func(int, time.Time)
	onError   func()

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewADSBLolListener creates a new adsb.lol polling adapter.
func NewADSBLolListener(cfg *config.Config, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics, onSuccess func(int, time.Time), onError func()) *ADSBLolListener {
	return &ADSBLolListener{
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
func (l *ADSBLolListener) Start() error {
	l.logger.Info("starting adsb.lol listener",
		zap.Int("interval_sec", l.cfg.ADSBLolIntervalSec),
	)

	l.metrics.ActiveConnections.WithLabelValues(models.SourceADSBLol).Inc()

	l.wg.Add(1)
	go l.pollLoop()

	return nil
}

// Stop signals the polling goroutine to exit and waits for it to finish.
func (l *ADSBLolListener) Stop() {
	l.stopOnce.Do(func() {
		l.logger.Info("stopping adsb.lol listener")
		close(l.stop)
		l.wg.Wait()
		l.metrics.ActiveConnections.WithLabelValues(models.SourceADSBLol).Dec()
		l.logger.Info("adsb.lol listener stopped")
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
				l.logger.Debug("adsb.lol backoff", zap.Duration("delay", backoff))
				select {
				case <-l.stop:
					return
				case <-time.After(backoff):
				}
			}

			if err := l.poll(); err != nil {
				l.logger.Warn("adsb.lol poll failed", zap.Error(err))
				l.metrics.MessagesFailed.WithLabelValues(models.SourceADSBLol, "poll_error").Inc()
				l.metrics.FeedErrorsTotal.WithLabelValues("adsblol").Inc()
				if l.onError != nil {
					l.onError()
				}

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

// poll fetches the current military aircraft from the adsb.lol API and sends
// each aircraft position to the pipeline input channel.
func (l *ADSBLolListener) poll() error {
	req, err := http.NewRequest(http.MethodGet, adsbLolBaseURL, nil)
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

	now := time.Now().UTC()
	sent := 0
	for _, ac := range aircraft {
		ep := adsbLolAircraftToEntityPosition(ac, now)
		if ep == nil {
			continue
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
			ReceivedAt: now,
		}

		select {
		case l.input <- msg:
			sent++
		default:
			l.logger.Warn("pipeline input full, dropping adsb.lol message",
				zap.String("entity_id", ep.EntityID),
			)
			l.metrics.MessagesFailed.WithLabelValues(models.SourceADSBLol, "queue_full").Inc()
		}
	}

	l.metrics.MessagesReceived.WithLabelValues(models.SourceADSBLol).Add(float64(sent))

	successTime := time.Now()
	l.metrics.FeedLastSuccess.WithLabelValues("adsblol").Set(float64(successTime.Unix()))
	l.metrics.FeedEntityCount.WithLabelValues("adsblol").Set(float64(sent))
	if l.onSuccess != nil {
		l.onSuccess(sent, successTime)
	}

	l.logger.Info("adsb.lol poll complete", zap.Int("aircraft", sent), zap.Int("total_ac", len(aircraft)))

	return nil
}

// -- adsb.lol response types --------------------------------------------------

type adsbLolResponse struct {
	AC []adsbLolAircraft `json:"ac"`
}

type adsbLolAircraft struct {
	Hex              string           `json:"hex"`
	Flight           *string          `json:"flight"`
	Reg              *string          `json:"r"`
	AircraftType     *string          `json:"t"`
	AircraftTypeName *string          `json:"desc"`
	OperatorName     *string          `json:"ownOp"`
	Lat              *float64         `json:"lat"`
	Lon              *float64         `json:"lon"`
	AltBaro          *adsbLolAltitude `json:"alt_baro"`
	AltGeom          *float64         `json:"alt_geom"`
	GroundSpeed      *float64         `json:"gs"`
	Track            *float64         `json:"track"`
	BaroRate         *float64         `json:"baro_rate"`
	Squawk           *string          `json:"squawk"`
	Category         *string          `json:"category"`
	NacP             *int             `json:"nac_p"`
	SIL              *int             `json:"sil"`
	NIC              *int             `json:"nic"`
	RC               *float64         `json:"rc"`
}

// adsbLolAltitude handles the alt_baro field which can be a number or the
// string "ground".
type adsbLolAltitude struct {
	Value    float64
	OnGround bool
}

func (a *adsbLolAltitude) UnmarshalJSON(data []byte) error {
	// Try number first.
	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		a.Value = num
		a.OnGround = false
		return nil
	}

	// Try string (e.g. "ground").
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "ground" {
			a.Value = 0
			a.OnGround = true
			return nil
		}
		return fmt.Errorf("unknown alt_baro string: %q", s)
	}

	return fmt.Errorf("cannot unmarshal alt_baro: %s", string(data))
}

// parseADSBLolResponse unmarshals the JSON body and returns the aircraft list.
func parseADSBLolResponse(body []byte) ([]adsbLolAircraft, error) {
	var resp adsbLolResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return resp.AC, nil
}

// adsbLolAircraftToEntityPosition converts a parsed aircraft to the canonical
// EntityPosition format. Returns nil if the aircraft has no valid position.
func adsbLolAircraftToEntityPosition(ac adsbLolAircraft, now time.Time) *models.EntityPosition {
	if ac.Lat == nil || ac.Lon == nil {
		return nil
	}

	icaoHex := strings.ToUpper(ac.Hex)
	ep := &models.EntityPosition{
		EntityID:         "ICAO-" + icaoHex,
		EntityType:       models.EntityTypeAircraft,
		Source:           models.SourceADSBLol,
		Latitude:         *ac.Lat,
		Longitude:        *ac.Lon,
		Timestamp:        now,
		TrackEnvironment: "AIR",
	}

	adsbData := &models.ADSBData{
		ICAOHex: icaoHex,
	}

	// Name: callsign (trimmed) if available, else hex.
	if ac.Flight != nil {
		name := strings.TrimSpace(*ac.Flight)
		if name != "" {
			ep.Name = name
			adsbData.AircraftID = name
		} else {
			ep.Name = ac.Hex
		}
	} else {
		ep.Name = ac.Hex
	}

	// Registration and aircraft type
	if ac.Reg != nil {
		adsbData.Registration = *ac.Reg
	}
	if ac.AircraftType != nil {
		adsbData.AircraftType = *ac.AircraftType
	}
	if ac.AircraftTypeName != nil {
		adsbData.AircraftTypeName = *ac.AircraftTypeName
	}
	if ac.OperatorName != nil {
		adsbData.OperatorName = *ac.OperatorName
	}
	if ac.Squawk != nil {
		adsbData.Squawk = *ac.Squawk
	}

	// Altitude: prefer baro (if numeric), fall back to geometric.
	if ac.AltBaro != nil {
		adsbData.OnGround = ac.AltBaro.OnGround
		if !ac.AltBaro.OnGround {
			ep.Altitude = ac.AltBaro.Value
			adsbData.AltitudeBaro = ac.AltBaro.Value
		}
	} else if ac.AltGeom != nil {
		ep.Altitude = *ac.AltGeom
	}
	if ac.AltGeom != nil {
		adsbData.AltitudeGeom = *ac.AltGeom
	}

	// Speed: gs is already in knots. Decompose into velocity components.
	if ac.GroundSpeed != nil {
		ep.SpeedKnots = *ac.GroundSpeed
		adsbData.GroundSpeed = *ac.GroundSpeed

		// Decompose into North-East using track angle
		if ac.Track != nil {
			gsMS := *ac.GroundSpeed / metersPerSecToKnots // knots → m/s
			trackRad := *ac.Track * math.Pi / 180.0
			ep.VelocityNorth = gsMS * math.Cos(trackRad)
			ep.VelocityEast = gsMS * math.Sin(trackRad)
		}
	}

	// Vertical rate: baro_rate is ft/min → m/s
	if ac.BaroRate != nil {
		ep.VelocityUp = *ac.BaroRate * 0.00508
		adsbData.VerticalRate = *ac.BaroRate * 0.00508
	}

	// Heading and course from track.
	if ac.Track != nil {
		ep.Heading = *ac.Track
		ep.Course = *ac.Track
	}

	// Quality indicators
	if ac.Category != nil {
		adsbData.Category = *ac.Category
	}
	if ac.NacP != nil {
		adsbData.NacP = *ac.NacP
	}
	if ac.SIL != nil {
		adsbData.SIL = *ac.SIL
	}
	if ac.NIC != nil {
		adsbData.NIC = *ac.NIC
	}
	if ac.RC != nil {
		adsbData.RC = *ac.RC
	}

	ep.ADSBData = adsbData

	return ep
}
