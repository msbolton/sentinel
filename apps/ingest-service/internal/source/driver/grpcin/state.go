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
	"github.com/sentinel/ingest-service/internal/source/driver/emplacementmirror"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// sensorEvery is the most often a mobile emplacement's own observation is
// emitted: its sender may report many times a second.
const sensorEvery = time.Second

// maxClockLead is how far ahead of the server a fix may be stamped: it
// allows for ordinary clock skew and nothing more, because the newest fix
// anchors the track's window and the ownship tolerance.
const maxClockLead = 5 * time.Second

var errBadState = errors.New("an emplacement_state needs a position inside WGS84 bounds, and finite heading and velocity, and a time within a minute of now")

// state takes a mobile emplacement's report of itself: it extends the
// emplacement's track (the origin of its measurements), records its
// position, and moves its SENSOR entity.
func (s *service) state(ctx context.Context, a authed, from string, r *ingestv1.Report, m *sentinelv1.EmplacementState, ack *ingestv1.Ack) (*ingestv1.Ack, error) {
	if m == nil {
		return s.deadLetter(ack, from, r, "empty_report", errors.New("the report's emplacement_state is empty")), nil
	}
	claim := r.GetEmplacementId()
	if id := m.GetEmplacementId(); id != "" {
		if claim != "" && claim != id {
			return s.reject(ack, "unknown_emplacement"), nil
		}
		claim = id
	}
	e, bound, reason := s.resolve(a, claim)
	if reason != "" || !bound || e.Mobility != emplacement.MobilityMobile {
		return s.reject(ack, "unknown_emplacement"), nil
	}
	if max(classification.Rank(s.d.floor), classification.Rank(e.Classification)) > classification.Rank(a.tok.Classification) {
		return s.reject(ack, "above_ceiling"), nil
	}
	now := s.d.now().UTC()
	f, ok := fixOf(m, now)
	if !ok {
		if classification.Rank(e.Classification) > classification.Rank(s.d.floor) {
			return s.reject(ack, "bad_state"), nil
		}
		return s.deadLetter(ack, from, r, "bad_state", errBadState), nil
	}
	s.d.addFix(e.ID, f)
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
	s.d.touch(ctx, e.ID, pos, alt, now, reportAt)
	// The SENSOR observation is rated on server time for the same reason.
	if s.d.sensorDue(e.ID, now) {
		e.Position, e.Altitude = pos, alt
		o := emplacementmirror.Observation(e, reportAt)
		o.Decoder = Decoder
		o.ReceivedAt = now
		if f.heading != nil {
			o.Heading = f.heading
		}
		o.Velocity = f.vel
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

// fixOf reads a state report, or reports that it is malformed.
func fixOf(m *sentinelv1.EmplacementState, now time.Time) (fix, bool) {
	p := m.GetPosition()
	if p == nil || !finite(p.GetLatitude()) || !finite(p.GetLongitude()) ||
		p.GetLatitude() < -90 || p.GetLatitude() > 90 || p.GetLongitude() < -180 || p.GetLongitude() > 180 {
		return fix{}, false
	}
	f := fix{at: now, pos: geo.LLA{Lat: p.GetLatitude(), Lon: p.GetLongitude()}}
	if p.AltitudeMeters != nil {
		if !finite(*p.AltitudeMeters) {
			return fix{}, false
		}
		f.pos.Alt = *p.AltitudeMeters
	}
	if t := m.GetTime(); t != nil {
		if t.CheckValid() != nil {
			return fix{}, false
		}
		f.at = t.AsTime().UTC()
		if f.at.Sub(now) > maxClockLead {
			return fix{}, false
		}
	}
	if m.Heading != nil {
		if !finite(*m.Heading) {
			return fix{}, false
		}
		h := math.Mod(math.Mod(*m.Heading, 360)+360, 360)
		f.heading = &h
	}
	if v := m.GetVelocity(); v != nil {
		if !finite(v.GetNorth()) || !finite(v.GetEast()) || !finite(v.GetUp()) {
			return fix{}, false
		}
		f.vel = &[3]float64{v.GetNorth(), v.GetEast(), v.GetUp()}
	}
	return f, true
}
