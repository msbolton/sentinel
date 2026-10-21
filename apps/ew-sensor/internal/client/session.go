package client

import (
	"context"
	"fmt"
	"math"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ew-sensor/internal/config"
)

// upsertDescription is the description of an emplacement the sensor creates.
const upsertDescription = "ew-sensor simulator"

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
		// Heartbeats are never rate limited: only the observations count.
		per := c.cfg.EmitterCount
		if want := c.cfg.Sensor.Rate * float64(per); want > float64(limit) {
			c.log.Warn("the configured rate can exceed the connection's message limit; expect rate_limited acks",
				"reports_per_sec_max", want, "max_messages_per_sec", limit)
		}
	}
	return sess, nil
}

// emplacement finds the emplacement to report as (spec F step 2): the one
// --emplacement-id names, else one upserted by name when the connection
// lets senders manage emplacements, else the connection's single active
// bound emplacement. It returns nil, nil when ctx ends first.
func (c *Client) emplacement(ctx context.Context, api ingestv1.IngestServiceClient, sess *ingestv1.GetSessionResponse) (*sentinelv1.Emplacement, error) {
	emps := sess.GetEmplacements()
	if id := strings.TrimSpace(c.cfg.Emplacement.EmplacementID); id != "" {
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

// upsert creates or updates the configured emplacement by name (ruling R12),
// retrying "rate limited" and Unavailable. The emplacement cap, a
// connection that is not sender-managed and a classification above the
// token are fatal. A success opens the unknown_emplacement grace window.
func (c *Client) upsert(ctx context.Context, api ingestv1.IngestServiceClient, sess *ingestv1.GetSessionResponse) (*sentinelv1.Emplacement, error) {
	req := &ingestv1.UpsertEmplacementRequest{Emplacement: upsertMessage(c.cfg, sess.GetExercise())}
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
			return fatalf(ExitAuth, "UpsertEmplacement refused (%s): %s; delete an emplacement or pass --emplacement-id", st.Code(), st.Message())
		case codes.Unauthenticated, codes.PermissionDenied:
			return fatalf(ExitAuth, "UpsertEmplacement refused (%s): %s", st.Code(), st.Message())
		case codes.AlreadyExists:
			return fatalf(ExitFatal, "UpsertEmplacement failed (%s): %s; emplacement names are unique, so pass --name <unique> to create a different one, or --emplacement-id to use the existing one",
				st.Code(), st.Message())
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

// upsertMessage is the emplacement the sensor upserts (ruling R12). It
// carries the connection's exercise flag: the service stores the flag as
// sent, and the emplacement's own SENSOR entity takes it from the row, which
// is what keeps a simulated site off an outbound connection.
func upsertMessage(cfg config.Config, exercise bool) *sentinelv1.Emplacement {
	e := cfg.Emplacement
	m := &sentinelv1.Emplacement{
		Name:             e.Name,
		Description:      upsertDescription,
		SensorType:       sentinelv1.SensorType_SENSOR_TYPE_RF,
		Mobility:         sentinelv1.Mobility(sentinelv1.Mobility_value["MOBILITY_"+strings.ToUpper(e.Mobility)]),
		Position:         &sentinelv1.Coordinate{Latitude: e.Lat, Longitude: e.Lon, AltitudeMeters: proto.Float64(e.Alt)},
		BoresightDeg:     e.Boresight,
		AzimuthReference: sentinelv1.AzimuthReference(sentinelv1.AzimuthReference_value["AZIMUTH_REFERENCE_"+strings.ToUpper(e.AzimuthReference)]),
		Coverage: &sentinelv1.Coverage{
			MinRangeM:       e.MinRangeKm * 1000,
			MaxRangeM:       e.RangeKm * 1000,
			AzimuthWidthDeg: e.Width,
		},
		Exercise: exercise,
	}
	if cls := cfg.Sensor.Classification; cls != "" {
		m.Classification = sentinelv1.Classification(sentinelv1.Classification_value["CLASSIFICATION_"+strings.ToUpper(cls)])
	}
	return m
}

// withServerGeometry returns cfg with the sensor site taken from a bound
// emplacement the server owns (ruling R7): position, boresight, azimuth
// reference, coverage and mobility. Values the server leaves unset keep
// the configured ones.
func withServerGeometry(cfg config.Config, e *sentinelv1.Emplacement) (config.Config, error) {
	em := &cfg.Emplacement
	em.EmplacementID = e.GetId()
	if n := e.GetName(); n != "" {
		em.Name = n
	}
	if p := e.GetPosition(); p != nil {
		em.Lat, em.Lon = p.GetLatitude(), p.GetLongitude()
		if p.AltitudeMeters != nil {
			em.Alt = p.GetAltitudeMeters()
		}
	} else if em.Lat == 0 && em.Lon == 0 {
		return cfg, fatalf(ExitFatal, "emplacement %s has no position: set one in Sentinel, or pass --lat and --lon", e.GetId())
	}
	em.Boresight = math.Mod(math.Mod(e.GetBoresightDeg(), 360)+360, 360)
	switch e.GetMobility() {
	case sentinelv1.Mobility_MOBILITY_FIXED:
		em.Mobility = config.MobilityFixed
	case sentinelv1.Mobility_MOBILITY_MOBILE:
		em.Mobility = config.MobilityMobile
	}
	switch e.GetAzimuthReference() {
	case sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH:
		em.AzimuthReference = config.AzimuthTrueNorth
	case sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_PLATFORM:
		em.AzimuthReference = config.AzimuthPlatform
	}
	if cv := e.GetCoverage(); cv.GetMaxRangeM() > 0 {
		em.RangeKm = cv.GetMaxRangeM() / 1000
		em.MinRangeKm = 0
		if mn := cv.GetMinRangeM(); mn > 0 && mn < cv.GetMaxRangeM() {
			em.MinRangeKm = mn / 1000
		}
		if w := cv.GetAzimuthWidthDeg(); w > 0 {
			em.Width = min(w, 360)
		}
	}
	return cfg, nil
}
