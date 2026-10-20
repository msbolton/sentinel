// Package emplacementmirror runs the built-in emplacements feed. It reports
// every emplacement as a SENSOR observation under the canonical EMPLACEMENT
// identity, so each one becomes an entity: found by search, usable as a
// geofence anchor and, for a mobile emplacement, tracked.
//
// An active emplacement with a position is reported when the driver starts
// and whenever it changes. Fixed ones are reported again every heartbeat so
// their entities stay live. A mobile one is not, because its position comes
// from its own reports. A mobile one bound to a connection is never reported
// here: that connection's position reports report it. Deactivating or
// deleting an emplacement stops its reports, and the entity ages out.
package emplacementmirror

import (
	"context"
	_ "embed"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

const (
	// ID is the driver id.
	ID = "emplacement"
	// Decoder is the decoder name written on the observations.
	Decoder = "emplacement"

	defaultHeartbeat = 60 * time.Second
)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "Emplacements", Internal: true,
		Description:  "Sentinel's own emplacements, reported as SENSOR entities. Runs the built-in emplacements feed.",
		ConfigSchema: schema,
		New:          New,
		// An edit (a new name, a raised level) keeps the position, so
		// suppressing repeats would drop it.
		NoRepeatSuppression: true,
	})
}

// Config is the feed configuration.
type Config struct {
	HeartbeatSec int `json:"heartbeatSec"`
}

// Driver reports emplacements.
type Driver struct {
	src       driver.EmplacementSource
	heartbeat time.Duration
	logger    *zap.Logger
	now       func() time.Time

	mu       sync.Mutex
	reported int64
	lastAt   time.Time
}

// New builds the driver; it needs the emplacement cache in deps.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	if deps.Emplacements == nil {
		return nil, fmt.Errorf("feed %s: the emplacement driver needs the emplacement cache", spec.FeedID)
	}
	return &Driver{src: deps.Emplacements, heartbeat: driver.Seconds(cfg.HeartbeatSec, defaultHeartbeat), logger: deps.Logger, now: time.Now}, nil
}

// Run reports until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	changes := make(chan string, 256)
	cancel := d.src.OnChange(func(id string) {
		select {
		case changes <- id:
		default: // full: the next heartbeat catches fixed ones up
		}
	})
	defer cancel()

	if err := d.report(ctx, sink, true); err != nil {
		return quiet(ctx, err)
	}
	ticker := time.NewTicker(d.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case id := <-changes:
			if e, ok := d.src.Get(id); ok && e.Active && e.Position != nil && !boundMobile(e) {
				if err := d.emit(ctx, sink, e); err != nil {
					return quiet(ctx, err)
				}
			}
		case <-ticker.C:
			if err := d.report(ctx, sink, false); err != nil {
				return quiet(ctx, err)
			}
		}
	}
}

// report emits every active emplacement with a position: fixed ones always,
// mobile ones only when withMobile (at start).
func (d *Driver) report(ctx context.Context, sink driver.Sink, withMobile bool) error {
	n := 0
	for _, e := range d.src.All() {
		if !e.Active || e.Position == nil || boundMobile(e) || (e.Mobility != emplacement.MobilityFixed && !withMobile) {
			continue
		}
		if err := d.emit(ctx, sink, e); err != nil {
			return err
		}
		n++
	}
	sink.Succeeded(n)
	return nil
}

// boundMobile is a mobile emplacement bound to a connection. That connection's
// reports place it; the stored position only goes stale between Touch writes,
// which send no notice.
func boundMobile(e emplacement.Emplacement) bool {
	return e.Mobility == emplacement.MobilityMobile && e.FeedID != nil
}

func (d *Driver) emit(ctx context.Context, sink driver.Sink, e emplacement.Emplacement) error {
	now := d.now().UTC()
	if err := sink.Emit(ctx, Observation(e, now)); err != nil {
		return err
	}
	d.mu.Lock()
	d.reported++
	d.lastAt = now
	d.mu.Unlock()
	return nil
}

// quiet turns the error a cancelled run returns into a normal stop.
func quiet(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// Health reports what the mirror has done.
func (d *Driver) Health() driver.Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	detail := map[string]any{"emplacements": len(d.src.All()), "reported": d.reported}
	if !d.lastAt.IsZero() {
		detail["lastReportAt"] = d.lastAt.Format(time.RFC3339)
	}
	return driver.Health{Connected: true, Detail: detail}
}

// Observation is the SENSOR report of e at time at.
func Observation(e emplacement.Emplacement, at time.Time) *obs.Observation {
	o := &obs.Observation{
		Decoder:          Decoder,
		SourceEntityID:   e.ID,
		Source:           SourceFor(e.SensorType),
		SourceTimestamp:  at,
		EntityType:       obs.EntitySensor,
		TrackEnvironment: obs.EnvGround,
		DisplayName:      e.Name,
		Classification:   e.Classification,
		EmplacementID:    e.ID,
		Exercise:         e.Exercise,
	}
	// A boresight from true north is the sensor's heading. One from the
	// platform is relative to the platform's own heading, which a mobile
	// emplacement's reports carry, so it is no heading of its own.
	if e.AzimuthReference == emplacement.AzimuthTrueNorth {
		o.Heading = obs.Float(e.BoresightDeg)
	}
	if e.Mobility == emplacement.MobilityMobile {
		o.TrackEnvironment = obs.EnvUnknown
	}
	o.AddIdentity(obs.IdentityEmplacement, e.ID)
	if e.Position != nil {
		o.SetPosition(e.Position.Lat, e.Position.Lon)
	}
	if e.Altitude != nil {
		o.Altitude = obs.Float(*e.Altitude)
	}
	return o
}

// SourceFor maps a sensor type to the EntitySource its reports carry.
func SourceFor(sensorType string) string {
	switch sensorType {
	case "RADAR":
		return "RADAR"
	case "AIS_RECEIVER":
		return "AIS"
	case "ADSB_RECEIVER":
		return "ADS_B"
	case "HUMAN":
		return "HUMINT"
	case "RF":
		return "SIGINT"
	}
	return "MASINT"
}
