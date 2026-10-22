package client

import (
	"context"
	"fmt"
	"math"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/geo"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/sensor-sim/internal/config"
)

// Descriptions of the emplacements the simulator creates.
const (
	radarDescription = "sensor-sim radar"
	eoirDescription  = "sensor-sim EO/IR camera"
	esmDescription   = "sensor-sim ESM"
)

// retrying calls rpc until it succeeds (true, nil), fatal says an error is
// fatal (false, that error), or ctx ends (false, nil). Every retry is logged
// with its gRPC code and message, since an allowlist refusal, a TLS mismatch
// and a disabled connection all look like Unavailable.
func (c *Client) retrying(ctx context.Context, name string, rpc func(context.Context) error, fatal func(*status.Status) error) (bool, error) {
	bo := newBackoff(c.opts.BackoffMin, c.opts.BackoffMax, c.opts.Jitter)
	for {
		cctx, cancel := context.WithTimeout(ctx, c.opts.RPCTimeout)
		err := rpc(cctx)
		cancel()
		if err == nil {
			return true, nil
		}
		if ctx.Err() != nil {
			return false, nil
		}
		st := status.Convert(err)
		if f := fatal(st); f != nil {
			c.log.Error(name+" failed", "code", st.Code().String(), "message", st.Message())
			return false, f
		}
		d := bo.next()
		c.log.Warn(name+" failed; retrying", "code", st.Code().String(), "message", st.Message(), "backoff", d)
		if !sleep(ctx, d) {
			return false, nil
		}
	}
}

// session calls GetSession, retrying Unavailable and transport errors.
// It returns nil, nil when ctx ends first.
func (c *Client) session(ctx context.Context, api ingestv1.IngestServiceClient) (*ingestv1.GetSessionResponse, error) {
	var sess *ingestv1.GetSessionResponse
	ok, err := c.retrying(ctx, "GetSession", func(ctx context.Context) error {
		var err error
		sess, err = api.GetSession(ctx, &ingestv1.GetSessionRequest{})
		return err
	}, func(st *status.Status) error {
		switch st.Code() {
		case codes.Unavailable, codes.DeadlineExceeded:
			return nil
		case codes.Unauthenticated, codes.PermissionDenied:
			return fatalf(ExitAuth, "GetSession refused (%s): %s", st.Code(), st.Message())
		}
		return fatalf(ExitFatal, "GetSession failed (%s): %s", st.Code(), st.Message())
	})
	if !ok {
		return nil, err
	}
	c.log.Info("session",
		"feed_id", sess.GetFeedId(),
		"emplacements", len(sess.GetEmplacements()),
		"classification_floor", sess.GetClassificationFloor().String(),
		"exercise", sess.GetExercise(),
		"sender_managed_emplacements", sess.GetSenderManagedEmplacements(),
		"max_message_bytes", sess.GetMaxMessageBytes(),
		"max_messages_per_sec", sess.GetMaxMessagesPerSec())
	if limit := sess.GetMaxMessagesPerSec(); limit > 0 {
		// Heartbeats are never rate limited: only the observations count, at
		// most one per target (an ESM sensor's: per emitter) per scan.
		per := len(c.cfg.Scenario.Targets)
		if c.cfg.ESM() {
			per = c.cfg.EmitterCount
		}
		if want := c.cfg.Sensor.Rate * float64(per); want > float64(limit) {
			c.log.Warn("the configured rate can exceed the connection's message limit; expect rate_limited acks",
				"reports_per_sec_max", want, "max_messages_per_sec", limit)
		}
	}
	return sess, nil
}

// emplacement finds the emplacement to report as. A radar or camera always
// creates its own, by name, from its configuration, so the connection must
// let senders manage emplacements. An ESM sensor reports as the one
// --emplacement-id names, else one it upserts by name when the connection
// lets senders manage emplacements, else the connection's single active
// bound emplacement. It returns nil, nil when ctx ends first.
func (c *Client) emplacement(ctx context.Context, api ingestv1.IngestServiceClient, sess *ingestv1.GetSessionResponse) (*sentinelv1.Emplacement, error) {
	if c.cfg.ESM() {
		return c.esmEmplacement(ctx, api, sess)
	}
	if !sess.GetSenderManagedEmplacements() {
		return nil, fatalf(ExitFatal, "connection %s does not let senders manage emplacements: sensor-sim creates its own, so turn on senderManagedEmplacements in the connection's settings",
			sess.GetFeedId())
	}
	e, err := c.upsert(ctx, api, sess)
	if e != nil {
		c.logEmplacement("upserted", e)
	}
	return e, err
}

func (c *Client) esmEmplacement(ctx context.Context, api ingestv1.IngestServiceClient, sess *ingestv1.GetSessionResponse) (*sentinelv1.Emplacement, error) {
	emps := sess.GetEmplacements()
	if id := strings.TrimSpace(c.cfg.Sensor.EmplacementID); id != "" {
		for _, e := range emps {
			if strings.EqualFold(strings.TrimSpace(e.GetId()), id) {
				c.logEmplacement("--emplacement-id", e)
				return e, nil
			}
		}
		return nil, fatalf(ExitFatal, "emplacement %q is not among the %d emplacements GetSession lists for connection %s: check the id, that it is bound to this connection, and that it is not above the token's classification",
			id, len(emps), sess.GetFeedId())
	}
	if sess.GetSenderManagedEmplacements() {
		e, err := c.upsert(ctx, api, sess)
		if e != nil {
			c.logEmplacement("upserted", e)
		}
		return e, err
	}
	var active []*sentinelv1.Emplacement
	for _, e := range emps {
		if e.GetActive() {
			active = append(active, e)
		}
	}
	switch len(active) {
	case 1:
		c.logEmplacement("single bound", active[0])
		return active[0], nil
	case 0:
		return nil, fatalf(ExitFatal, "connection %s has no active bound emplacement and does not let senders manage emplacements: bind one, enable senderManagedEmplacements, or pass --emplacement-id",
			sess.GetFeedId())
	}
	names := make([]string, 0, len(active))
	for _, e := range active {
		names = append(names, fmt.Sprintf("%s (%s)", e.GetName(), e.GetId()))
	}
	return nil, fatalf(ExitFatal, "connection %s has %d active emplacements [%s]: pass --emplacement-id to choose one",
		sess.GetFeedId(), len(active), strings.Join(names, ", "))
}

func (c *Client) logEmplacement(source string, e *sentinelv1.Emplacement) {
	if !e.GetActive() {
		c.log.Warn("the emplacement is inactive: the service will refuse its reports (unknown_emplacement)", "emplacement_id", e.GetId())
	}
	c.log.Info("emplacement", "source", source, "emplacement_id", e.GetId(), "name", e.GetName(),
		"mobility", e.GetMobility().String(), "active", e.GetActive())
}

// upsert creates or updates the configured emplacement by name,
// retrying "rate limited" and Unavailable. The emplacement cap, a
// connection that is not sender-managed and a classification above the
// token are fatal. A success opens the unknown_emplacement grace window.
func (c *Client) upsert(ctx context.Context, api ingestv1.IngestServiceClient, sess *ingestv1.GetSessionResponse) (*sentinelv1.Emplacement, error) {
	req := &ingestv1.UpsertEmplacementRequest{Emplacement: upsertMessage(c.cfg, c.src.Site, sess.GetExercise())}
	// An ESM sensor can also report as an existing emplacement.
	orID, orUseID := "", ""
	if c.cfg.ESM() {
		orID, orUseID = ", or pass --emplacement-id", ", or --emplacement-id to use the existing one"
	}
	var resp *ingestv1.UpsertEmplacementResponse
	ok, err := c.retrying(ctx, "UpsertEmplacement", func(ctx context.Context) error {
		var err error
		resp, err = api.UpsertEmplacement(ctx, req)
		return err
	}, func(st *status.Status) error {
		switch st.Code() {
		case codes.Unavailable, codes.DeadlineExceeded:
			return nil
		case codes.ResourceExhausted:
			if strings.Contains(st.Message(), "rate limited") {
				return nil
			}
			return fatalf(ExitAuth, "UpsertEmplacement refused (%s): %s; delete an emplacement or raise the connection's maxEmplacements%s", st.Code(), st.Message(), orID)
		case codes.Unauthenticated, codes.PermissionDenied:
			return fatalf(ExitAuth, "UpsertEmplacement refused (%s): %s", st.Code(), st.Message())
		case codes.AlreadyExists:
			return fatalf(ExitFatal, "UpsertEmplacement failed (%s): %s; emplacement names are unique, so pass --name <unique> to create a different one%s",
				st.Code(), st.Message(), orUseID)
		}
		return fatalf(ExitFatal, "UpsertEmplacement failed (%s): %s", st.Code(), st.Message())
	})
	if !ok {
		return nil, err
	}
	e := resp.GetEmplacement()
	if e.GetId() == "" {
		return nil, fatalf(ExitFatal, "UpsertEmplacement returned no emplacement id")
	}
	c.mu.Lock()
	c.graceUntil = c.opts.Now().Add(c.opts.GraceWindow)
	c.mu.Unlock()
	return e, nil
}

// upsertMessage is the FIXED emplacement the sensor upserts: a radar or an
// EO/IR camera at site (the resolved altitude, so the server places
// measurements from the same origin the simulator used), its mounting height
// when one was configured, and its coverage. A camera always declares its
// elevation window, defaults included; a radar only the limits it was given.
// It carries the connection's exercise flag: the service stores the flag as
// sent, and the emplacement's own SENSOR entity takes it from the row, which
// is what keeps a simulated site off an outbound connection.
func upsertMessage(cfg config.Config, site geo.LLA, exercise bool) *sentinelv1.Emplacement {
	if cfg.ESM() {
		return esmUpsertMessage(cfg, site, exercise)
	}
	s := cfg.Sensor
	camera := strings.EqualFold(s.Kind, config.KindEOIR)
	m := &sentinelv1.Emplacement{
		Name:             s.Name,
		Description:      radarDescription,
		SensorType:       sentinelv1.SensorType_SENSOR_TYPE_RADAR,
		Mobility:         sentinelv1.Mobility_MOBILITY_FIXED,
		Position:         &sentinelv1.Coordinate{Latitude: site.Lat, Longitude: site.Lon, AltitudeMeters: proto.Float64(site.Alt)},
		BoresightDeg:     s.Boresight,
		AzimuthReference: sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH,
		Coverage: &sentinelv1.Coverage{
			MinRangeM:       s.MinRangeKm * 1000,
			MaxRangeM:       s.RangeKm * 1000,
			AzimuthWidthDeg: s.Width,
		},
		Exercise: exercise,
	}
	if camera {
		m.Description = eoirDescription
		m.SensorType = sentinelv1.SensorType_SENSOR_TYPE_EO_IR
	}
	lo, hi := s.ElevationWindow()
	if camera || s.ElevationMinDeg != nil {
		m.Coverage.ElevationMinDeg = proto.Float64(lo)
	}
	if camera || s.ElevationMaxDeg != nil {
		m.Coverage.ElevationMaxDeg = proto.Float64(hi)
	}
	if h := s.HeightAboveGroundM; h != nil {
		m.HeightAboveGroundM = proto.Float64(*h)
	}
	return m
}

// esmUpsertMessage is the RF emplacement an ESM sensor upserts: FIXED or
// MOBILE, its azimuth reference, its coverage sector (no elevation limits)
// and its classification when one is set. site is the configured position
// (a mobile sensor's start).
func esmUpsertMessage(cfg config.Config, site geo.LLA, exercise bool) *sentinelv1.Emplacement {
	s := cfg.Sensor
	m := &sentinelv1.Emplacement{
		Name:             s.Name,
		Description:      esmDescription,
		SensorType:       sentinelv1.SensorType_SENSOR_TYPE_RF,
		Mobility:         sentinelv1.Mobility(sentinelv1.Mobility_value["MOBILITY_"+strings.ToUpper(s.Mobility)]),
		Position:         &sentinelv1.Coordinate{Latitude: site.Lat, Longitude: site.Lon, AltitudeMeters: proto.Float64(site.Alt)},
		BoresightDeg:     s.Boresight,
		AzimuthReference: sentinelv1.AzimuthReference(sentinelv1.AzimuthReference_value["AZIMUTH_REFERENCE_"+strings.ToUpper(s.AzimuthReference)]),
		Coverage: &sentinelv1.Coverage{
			MinRangeM:       s.MinRangeKm * 1000,
			MaxRangeM:       s.RangeKm * 1000,
			AzimuthWidthDeg: s.Width,
		},
		Exercise: exercise,
	}
	if cls := s.Classification; cls != "" {
		m.Classification = sentinelv1.Classification(sentinelv1.Classification_value["CLASSIFICATION_"+strings.ToUpper(cls)])
	}
	return m
}

// withServerGeometry returns cfg with the ESM sensor's site taken from a
// bound emplacement the server owns: position, boresight, azimuth reference,
// coverage and mobility. Values the server leaves unset keep the configured
// ones.
func withServerGeometry(cfg config.Config, e *sentinelv1.Emplacement) (config.Config, error) {
	s := &cfg.Sensor
	s.EmplacementID = e.GetId()
	if n := e.GetName(); n != "" {
		s.Name = n
	}
	if p := e.GetPosition(); p != nil {
		s.Lat, s.Lon = p.GetLatitude(), p.GetLongitude()
		if p.AltitudeMeters != nil {
			s.Alt = proto.Float64(p.GetAltitudeMeters())
		}
	} else if s.Lat == 0 && s.Lon == 0 {
		return cfg, fatalf(ExitFatal, "emplacement %s has no position: set one in Sentinel, or pass --lat and --lon", e.GetId())
	}
	s.Boresight = math.Mod(math.Mod(e.GetBoresightDeg(), 360)+360, 360)
	switch e.GetMobility() {
	case sentinelv1.Mobility_MOBILITY_FIXED:
		s.Mobility = config.MobilityFixed
	case sentinelv1.Mobility_MOBILITY_MOBILE:
		s.Mobility = config.MobilityMobile
	}
	switch e.GetAzimuthReference() {
	case sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH:
		s.AzimuthReference = config.AzimuthTrueNorth
	case sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_PLATFORM:
		s.AzimuthReference = config.AzimuthPlatform
	}
	if cv := e.GetCoverage(); cv.GetMaxRangeM() > 0 {
		s.RangeKm = cv.GetMaxRangeM() / 1000
		s.MinRangeKm = 0
		if mn := cv.GetMinRangeM(); mn > 0 && mn < cv.GetMaxRangeM() {
			s.MinRangeKm = mn / 1000
		}
		if w := cv.GetAzimuthWidthDeg(); w > 0 {
			s.Width = min(w, 360)
		}
	}
	return cfg, nil
}
