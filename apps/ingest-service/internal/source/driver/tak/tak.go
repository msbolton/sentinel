// Package tak is the bidirectional TAK Server connection: one TCP or TLS
// session to the server's streaming port. Inbound, every Cursor on Target
// event becomes an observation through the cot decoder; outbound, the
// pump's updates become CoT tracks, alerts and shapes through the cot
// encoder. The session is the driver's own, so a dropped socket pauses the
// pump (ErrNotConnected) rather than failing deliveries.
package tak

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/source/decoder"
	cotdec "github.com/sentinel/ingest-service/internal/source/decoder/cot"
	"github.com/sentinel/ingest-service/internal/source/driver"
	cotenc "github.com/sentinel/ingest-service/internal/source/encoder/cot"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// ID is the driver id.
const ID = "tak"

const (
	dialTimeout  = 10 * time.Second
	writeTimeout = 10 * time.Second
	maxBackoff   = 30 * time.Second
)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "TAK Server", Source: "COT", Direction: driver.DirectionBoth,
		Description:  "One session to a TAK Server: CoT events in as observations, entities, alerts and shapes out as CoT.",
		SecretFields: []string{"clientCert", "clientKey", "caCert"},
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration (plus the common direction key).
type Config struct {
	Address        string `json:"address"`
	SiteID         string `json:"siteId"`
	Direction      string `json:"direction"`
	TLS            *struct {
		Enabled            bool   `json:"enabled"`
		ServerName         string `json:"serverName"`
		InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	} `json:"tls"`
	Protocol       string `json:"protocol"`
	HeartbeatSec   int    `json:"heartbeatSec"`
	ReadTimeoutSec *int   `json:"readTimeoutSec"`
	MaxFrameBytes  int    `json:"maxFrameBytes"`
	Tracks         struct {
		StaleSec int    `json:"staleSec"`
		How      string `json:"how"`
	} `json:"tracks"`
	Alerts struct {
		StaleSec             int     `json:"staleSec"`
		EmergencyMinSeverity *string `json:"emergencyMinSeverity"`
	} `json:"alerts"`
	Shapes struct {
		Enabled     *bool `json:"enabled"`
		RefreshSec  int   `json:"refreshSec"`
		StrokeColor int32 `json:"strokeColor"`
		FillColor   int32 `json:"fillColor"`
	} `json:"shapes"`
}

// Driver is one TAK connection.
type Driver struct {
	cfg     Config
	logger  *zap.Logger
	tls     *tls.Config
	enc     *cotenc.Encoder
	dec     decoder.Decoder
	shapes  driver.ShapeSource
	inbound bool
	now     func() time.Time
	// dial is replaceable in tests.
	dial func(ctx context.Context) (net.Conn, error)

	mu        sync.Mutex
	conn      net.Conn
	sink      driver.Sink
	lastErr   string
	sent      int64
	received  int64
	connects  int64
	known     map[string]*sentinelv1.Entity // exported entities by id, for final events and deletes
	shapeUIDs map[string]bool
	alertUIDs map[string]bool // alerts sent as emergencies, for cancellation
}

// New validates the config and builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, errors.New("address is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, errors.New("address must be host:port")
	}
	if cfg.SiteID == "" {
		cfg.SiteID = "sentinel"
	}
	if cfg.Protocol != "" && cfg.Protocol != "xml" {
		return nil, fmt.Errorf("protocol %q is not supported", cfg.Protocol)
	}
	tlsCfg, err := buildTLS(cfg, spec.Secrets)
	if err != nil {
		return nil, err
	}
	logger := deps.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	encOpts := cotenc.Options{
		SiteID:     cfg.SiteID,
		TrackStale: time.Duration(cfg.Tracks.StaleSec) * time.Second,
		AlertStale: time.Duration(cfg.Alerts.StaleSec) * time.Second,
		How:        cfg.Tracks.How,
		StrokeColor: cfg.Shapes.StrokeColor, FillColor: cfg.Shapes.FillColor,
		EmergencyMinSeverity: "CRITICAL",
	}
	if cfg.Alerts.EmergencyMinSeverity != nil {
		encOpts.EmergencyMinSeverity = *cfg.Alerts.EmergencyMinSeverity
	}
	dec, err := cotdec.New(json.RawMessage(fmt.Sprintf(`{"skipSite":%q}`, cfg.SiteID)))
	if err != nil {
		return nil, err
	}
	dir := driver.Direction(cfg.Direction).Normalize()
	if cfg.Direction == "" {
		dir = driver.DirectionBoth
	}
	d := &Driver{
		cfg: cfg, logger: logger, tls: tlsCfg, enc: cotenc.New(encOpts), dec: dec, shapes: deps.Shapes,
		inbound: dir.Inbound(), now: time.Now,
		known: map[string]*sentinelv1.Entity{}, shapeUIDs: map[string]bool{}, alertUIDs: map[string]bool{},
	}
	d.dial = d.dialServer
	return d, nil
}

// buildTLS turns the PEM secrets into a TLS config; nil when TLS is off.
func buildTLS(cfg Config, secrets map[string]string) (*tls.Config, error) {
	if cfg.TLS == nil || !cfg.TLS.Enabled {
		return nil, nil
	}
	t := &tls.Config{ServerName: cfg.TLS.ServerName, InsecureSkipVerify: cfg.TLS.InsecureSkipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // operator choice
	if ca := pem(secrets["caCert"]); ca != nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, errors.New("caCert holds no certificates")
		}
		t.RootCAs = pool
	}
	cert, key := pem(secrets["clientCert"]), pem(secrets["clientKey"])
	if (cert == nil) != (key == nil) {
		return nil, errors.New("clientCert and clientKey must be given together")
	}
	if cert != nil {
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("client certificate: %w", err)
		}
		t.Certificates = []tls.Certificate{pair}
	}
	return t, nil
}

// pem returns PEM bytes from a secret that is PEM text or base64 of it.
func pem(v string) []byte {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if strings.HasPrefix(v, "-----BEGIN") {
		return []byte(v)
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil && strings.HasPrefix(string(b), "-----BEGIN") {
		return b
	}
	return []byte(v)
}

func (d *Driver) dialServer(ctx context.Context) (net.Conn, error) {
	nd := net.Dialer{Timeout: dialTimeout}
	if d.tls != nil {
		td := tls.Dialer{NetDialer: &nd, Config: d.tls}
		return td.DialContext(ctx, "tcp", d.cfg.Address)
	}
	return nd.DialContext(ctx, "tcp", d.cfg.Address)
}

// Run keeps one session open until ctx ends, reconnecting with backoff.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	d.mu.Lock()
	d.sink = sink
	d.mu.Unlock()
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		conn, err := d.dial(ctx)
		if err != nil {
			d.setError(err)
			backoff = transport.Backoff(backoff, maxBackoff)
			if transport.Sleep(ctx, backoff) != nil {
				return nil
			}
			continue
		}
		backoff = 0
		d.session(ctx, conn)
		if ctx.Err() != nil {
			return nil
		}
		if transport.Sleep(ctx, time.Second) != nil {
			return nil
		}
	}
	return nil
}

// session runs one connection: reader, heartbeat and shape refresher, until
// any of them fails or ctx ends.
func (d *Driver) session(ctx context.Context, conn net.Conn) {
	d.mu.Lock()
	d.conn = conn
	d.lastErr = ""
	d.connects++
	d.mu.Unlock()
	d.logger.Info("tak connected", zap.String("address", d.cfg.Address), zap.Bool("tls", d.tls != nil))

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		d.read(sctx, conn)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.heartbeat(sctx)
	}()
	if d.shapesOn() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.refreshShapes(sctx)
		}()
	}
	<-sctx.Done()
	conn.Close()
	wg.Wait()
	d.mu.Lock()
	if d.conn == conn {
		d.conn = nil
	}
	d.shapeUIDs = map[string]bool{} // the next session republishes everything
	d.mu.Unlock()
}

// read scans events off the socket and decodes them into observations.
func (d *Driver) read(ctx context.Context, conn net.Conn) {
	timeout := 75
	if d.cfg.ReadTimeoutSec != nil {
		timeout = *d.cfg.ReadTimeoutSec
	}
	cfg := transport.Config{Framing: "xmlevent", MaxFrameBytes: d.cfg.MaxFrameBytes, ReadTimeoutSec: timeout}
	err := transport.ScanConn(ctx, conn, cfg, d.emit)
	if err != nil && ctx.Err() == nil {
		d.setError(err)
	}
}

func (d *Driver) emit(ctx context.Context, f transport.Frame) error {
	d.mu.Lock()
	d.received++
	sink := d.sink
	d.mu.Unlock()
	if !d.inbound || sink == nil {
		return nil
	}
	observations, err := d.dec.Decode(f)
	if err != nil {
		if reason := decoder.SkipReason(err); reason != "" {
			sink.Skipped(reason)
			return nil
		}
		sink.DeadLetter(f.Payload, "parse_error", err)
		return nil
	}
	for _, o := range observations {
		if err := sink.Emit(ctx, o); err != nil {
			return err
		}
	}
	sink.Succeeded(len(observations))
	return nil
}

func (d *Driver) heartbeat(ctx context.Context) {
	interval := time.Duration(d.cfg.HeartbeatSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b, err := d.enc.Ping(d.now())
			if err == nil {
				_ = d.write(b)
			}
		}
	}
}

func (d *Driver) shapesOn() bool {
	return d.shapes != nil && (d.cfg.Shapes.Enabled == nil || *d.cfg.Shapes.Enabled)
}

// refreshShapes republishes geofences and locations on an interval and
// deletes the ones that went away.
func (d *Driver) refreshShapes(ctx context.Context) {
	interval := time.Duration(d.cfg.Shapes.RefreshSec) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}
	for {
		d.publishShapes(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (d *Driver) publishShapes(ctx context.Context) {
	now := d.now()
	current := map[string]bool{}
	fences, err := d.shapes.Geofences(ctx)
	if err != nil {
		d.logger.Warn("geofences unavailable", zap.Error(err))
	}
	for _, g := range fences {
		uid := d.enc.GeofenceUID(g.ID)
		if b, err := d.enc.Polygon(uid, g.Name, g.Points, now); err == nil && d.write(b) == nil {
			current[uid] = true
		}
	}
	locs, err := d.shapes.Locations(ctx)
	if err != nil {
		d.logger.Warn("locations unavailable", zap.Error(err))
	}
	for _, l := range locs {
		uid := d.enc.LocationUID(l.ID)
		if b, err := d.enc.Marker(uid, l.Name, l.Description, l.Lat, l.Lon, now); err == nil && d.write(b) == nil {
			current[uid] = true
		}
	}
	d.mu.Lock()
	gone := []string{}
	for uid := range d.shapeUIDs {
		if !current[uid] {
			gone = append(gone, uid)
		}
	}
	d.shapeUIDs = current
	d.mu.Unlock()
	for _, uid := range gone {
		if b, err := d.enc.Delete(uid, now); err == nil {
			_ = d.write(b)
		}
	}
}

// HandleEntity publishes an entity snapshot or removes one.
func (d *Driver) HandleEntity(_ context.Context, u driver.EntityUpdate) error {
	now := d.now()
	switch u.Kind {
	case driver.UpsertEntity:
		if u.Entity == nil {
			return nil
		}
		b, err := d.enc.Track(u.Entity, now, false)
		if err != nil {
			return err
		}
		if err := d.write(b); err != nil {
			return err
		}
		d.mu.Lock()
		d.known[u.EntityID] = u.Entity
		d.mu.Unlock()
		return nil
	case driver.RemoveEntity:
		d.mu.Lock()
		last := d.known[u.EntityID]
		delete(d.known, u.EntityID)
		d.mu.Unlock()
		if last == nil {
			return nil
		}
		var b []byte
		var err error
		if u.Reason == "deleted" {
			b, err = d.enc.Delete(d.enc.EntityUID(last), now)
		} else {
			// Aged out or left the policy: a final event with a short stale
			// window lets clients grey it out and drop it without the
			// thrash a hard delete causes at a box edge.
			b, err = d.enc.Track(last, now, true)
		}
		if err != nil {
			return err
		}
		return d.write(b)
	}
	return nil
}

// HandleAlert publishes a triggered alert and cancels a resolved emergency.
func (d *Driver) HandleAlert(_ context.Context, ev *sentinelv1.AlertEvent) error {
	if ev == nil || ev.Alert == nil {
		return nil
	}
	now := d.now()
	switch ev.EventType {
	case sentinelv1.AlertEventType_ALERT_EVENT_TYPE_TRIGGERED:
		d.mu.Lock()
		entityUID := ""
		if en := d.known[ev.Alert.EntityId]; en != nil {
			entityUID = d.enc.EntityUID(en)
		}
		d.mu.Unlock()
		b, err := d.enc.Alert(ev.Alert, entityUID, now)
		if err != nil {
			return err
		}
		if err := d.write(b); err != nil {
			return err
		}
		if strings.Contains(string(b), `type="b-a-o-tbl"`) {
			d.mu.Lock()
			d.alertUIDs[ev.Alert.AlertId] = true
			d.mu.Unlock()
		}
		return nil
	case sentinelv1.AlertEventType_ALERT_EVENT_TYPE_RESOLVED:
		d.mu.Lock()
		emergency := d.alertUIDs[ev.Alert.AlertId]
		delete(d.alertUIDs, ev.Alert.AlertId)
		d.mu.Unlock()
		if emergency {
			if b, err := d.enc.AlertCancel(ev.Alert, now); err == nil {
				if err := d.write(b); err != nil {
					return err
				}
			}
		}
		b, err := d.enc.Delete(d.enc.AlertUID(ev.Alert.AlertId), now)
		if err != nil {
			return err
		}
		return d.write(b)
	}
	return nil
}

// write sends one event; a failure closes the session so Run reconnects,
// and reports ErrNotConnected so the pump keeps the update.
func (d *Driver) write(b []byte) error {
	d.mu.Lock()
	conn := d.conn
	d.mu.Unlock()
	if conn == nil {
		return driver.ErrNotConnected
	}
	_ = conn.SetWriteDeadline(d.now().Add(writeTimeout))
	if _, err := conn.Write(b); err != nil {
		d.setError(err)
		conn.Close()
		return driver.ErrNotConnected
	}
	d.mu.Lock()
	d.sent++
	d.mu.Unlock()
	return nil
}

func (d *Driver) setError(err error) {
	d.mu.Lock()
	d.lastErr = err.Error()
	d.mu.Unlock()
}

// Health reports the session.
func (d *Driver) Health() driver.Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	return driver.Health{
		Connected: d.conn != nil,
		LastError: d.lastErr,
		Detail: map[string]any{
			"remotePeer": d.cfg.Address, "tls": d.tls != nil, "protocol": "xml",
			"eventsSent": d.sent, "eventsReceived": d.received, "connects": d.connects, "exported": len(d.known),
			"direction": directionLabel(d.inbound),
		},
	}
}

func directionLabel(inbound bool) string {
	if inbound {
		return "both"
	}
	return "out"
}
