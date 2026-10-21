package grpcin

import (
	"context"
	"errors"
	"math"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/ingest-service/internal/emplacement"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// sensorEvery is the most often a mobile emplacement's own observation is
// emitted: its sender may report many times a second.
const sensorEvery = time.Second

// maxClockLead is how far ahead of the server a fix may be stamped: it
// allows for ordinary clock skew and nothing more, because the newest fix
// anchors the track's window and the ownship tolerance. A fix more than
// ownshipWindow behind the server is as unusable: it falls outside the
// window its own track keeps. Either is refused as clock_skew, a sender's
// clock to mend, and not a malformed report.
const maxClockLead = 5 * time.Second

var (
	errBadState    = errors.New("an emplacement_state needs a position inside WGS84 bounds, and finite heading and velocity, and a valid time")
	errBadInterval = errors.New("an emplacement_state's heartbeat_interval_sec must be 0 (the default, 10) to 300")
)

// state takes an emplacement's heartbeat. For a fixed emplacement that is all
// it is: its position and time are not read. A mobile one's also extends its
// track (the origin of its measurements), records its position, and moves its
// SENSOR entity. Either way the SENSOR entity goes out online, at most once a
// second, stamped and throttled on the server's clock.
func (s *service) state(ctx context.Context, a authed, from string, r *ingestv1.Report, m *sentinelv1.EmplacementState, ack *ingestv1.Ack) (*ingestv1.Ack, error) {
	// Only a state taken as a heartbeat is exempt from the rate limits: every
	// refusal is charged first, so malformed states cannot flood the dead
	// letters.
	refuse := func(reason string) *ingestv1.Ack {
		if s.limited(from) {
			reason = "rate_limited"
		}
		return s.reject(ack, reason)
	}
	if m == nil {
		if s.limited(from) {
			return s.reject(ack, "rate_limited"), nil
		}
		return s.deadLetter(ack, from, r, "empty_report", errors.New("the report's emplacement_state is empty")), nil
	}
	claim := claimOf(r.GetEmplacementId())
	if id := claimOf(m.GetEmplacementId()); id != "" {
		if claim != "" && claim != id {
			return refuse("unknown_emplacement"), nil
		}
		claim = id
	}
	e, bound, reason := s.resolve(a, claim)
	if reason != "" || !bound {
		return refuse("unknown_emplacement"), nil
	}
	ack.EmplacementId = e.ID
	if max(classification.Rank(s.d.floor), classification.Rank(e.Classification)) > classification.Rank(a.tok.Classification) {
		return refuse("above_ceiling"), nil
	}
	interval, ok := emplacement.IntervalOf(m.GetHeartbeatIntervalSec())
	if !ok {
		return s.badState(ack, from, r, e, errBadInterval), nil
	}
	now := s.d.now().UTC()
	if e.Mobility != emplacement.MobilityMobile {
		s.d.beat(ctx, e.ID, interval, now)
		if s.d.sensorDue(e.ID, now) {
			if o, ok := s.d.siteFor(e, emplacement.HeartbeatOnline, now); ok {
				if raw, err := proto.Marshal(m); err == nil {
					o.Raw, o.RawContentType = raw, contentType
				}
				if err := s.sink.Emit(ctx, o); err != nil {
					return nil, err
				}
				s.sink.Succeeded(1)
			}
		}
		s.d.reportsOK.Add(1)
		ack.Status = ingestv1.AckStatus_ACK_STATUS_OK
		return ack, nil
	}
	f, why := fixOf(m, now)
	switch why {
	case "":
	case "clock_skew":
		return refuse(why), nil
	default:
		return s.badState(ack, from, r, e, errBadState), nil
	}
	s.d.addFix(e.ID, f)
	s.d.beat(ctx, e.ID, interval, now)
	pos := &emplacement.Point{Lat: f.pos.Lat, Lon: f.pos.Lon}
	var alt *float64
	if m.GetPosition().AltitudeMeters != nil {
		alt = &f.pos.Alt
	}
	// The write is throttled on server time, and records the fix time, never
	// a later one: a sender's clock must not stall or flood the writes.
	reportAt := f.at
	if reportAt.After(now) {
		reportAt = now
	}
	// A position reported over a connection above the emplacement's own level
	// must not land in its row: only lastReportAt is written.
	touchPos, touchAlt := pos, alt
	if classification.Rank(e.Classification) < classification.Rank(s.d.floor) {
		touchPos, touchAlt = nil, nil
	}
	s.d.touch(ctx, e.ID, touchPos, touchAlt, now, reportAt)
	// The SENSOR observation is rated on server time for the same reason.
	if s.d.sensorDue(e.ID, now) {
		e.Position, e.Altitude = pos, alt
		o := emplacement.Observation(e, reportAt)
		o.Decoder = Decoder
		o.ReceivedAt = now
		if f.heading != nil {
			o.Heading = f.heading
		}
		o.Velocity = f.vel
		emplacement.MarkSite(o, emplacement.HeartbeatOnline, now, interval)
		if raw, err := proto.Marshal(m); err == nil {
			o.Raw, o.RawContentType = raw, contentType
		}
		if err := s.sink.Emit(ctx, o); err != nil {
			return nil, err
		}
		s.sink.Succeeded(1)
	}
	s.d.reportsOK.Add(1)
	ack.Status = ingestv1.AckStatus_ACK_STATUS_OK
	return ack, nil
}

// badState refuses a malformed state. It is rejected when the emplacement is
// above the connection's level, so its content stays out of the dead
// letters (gated at the connection's level), and dead-lettered otherwise.
func (s *service) badState(ack *ingestv1.Ack, from string, r *ingestv1.Report, e emplacement.Emplacement, cause error) *ingestv1.Ack {
	if s.limited(from) {
		return s.reject(ack, "rate_limited")
	}
	if classification.Rank(e.Classification) > classification.Rank(s.d.floor) {
		return s.reject(ack, "bad_state")
	}
	return s.deadLetter(ack, from, r, "bad_state", cause)
}

// fixOf reads a state report. why is "" for a usable one, "bad_state" for a
// malformed one, and "clock_skew" for one stamped more than maxClockLead
// ahead of now or more than ownshipWindow behind it (a malformed report is
// bad_state whatever its time).
func fixOf(m *sentinelv1.EmplacementState, now time.Time) (f fix, why string) {
	p := m.GetPosition()
	if p == nil || !finite(p.GetLatitude()) || !finite(p.GetLongitude()) ||
		p.GetLatitude() < -90 || p.GetLatitude() > 90 || p.GetLongitude() < -180 || p.GetLongitude() > 180 {
		return fix{}, "bad_state"
	}
	f = fix{at: now, pos: geo.LLA{Lat: p.GetLatitude(), Lon: p.GetLongitude()}}
	if p.AltitudeMeters != nil {
		if !finite(*p.AltitudeMeters) {
			return fix{}, "bad_state"
		}
		f.pos.Alt = *p.AltitudeMeters
	}
	skewed := false
	if t := m.GetTime(); t != nil {
		if t.CheckValid() != nil {
			return fix{}, "bad_state"
		}
		f.at = t.AsTime().UTC()
		skewed = f.at.Sub(now) > maxClockLead || now.Sub(f.at) > ownshipWindow
	}
	if m.Heading != nil {
		if !finite(*m.Heading) {
			return fix{}, "bad_state"
		}
		h := math.Mod(math.Mod(*m.Heading, 360)+360, 360)
		f.heading = &h
	}
	if v := m.GetVelocity(); v != nil {
		if !finite(v.GetNorth()) || !finite(v.GetEast()) || !finite(v.GetUp()) {
			return fix{}, "bad_state"
		}
		f.vel = &[3]float64{v.GetNorth(), v.GetEast(), v.GetUp()}
	}
	if skewed {
		return fix{}, "clock_skew"
	}
	return f, ""
}
