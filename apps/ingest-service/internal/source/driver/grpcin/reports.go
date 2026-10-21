package grpcin

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/decoder/sentinelobs"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

const contentType = "application/x-protobuf"

var statusExpired = status.Error(codes.Unauthenticated, "the ingest token has expired")

// StreamReports acks each report in order, once the sink has taken it, so
// HTTP/2 flow control carries the publisher's backpressure to the sender.
//
// Known limit: a stream ended by a revocation or an expiry whose sender has
// stopped reading acks can stay blocked in Send, because grpc's write quota
// waits on the transport stream, not on the call context. Such a sender
// keeps its stream slot until it disconnects or the connection stops.
func (s *service) StreamReports(stream grpc.BidiStreamingServer[ingestv1.StreamReportsRequest, ingestv1.StreamReportsResponse]) error {
	ctx := stream.Context()
	a := caller(ctx)
	from := peerOf(ctx)
	reports := make(chan *ingestv1.Report)
	recvErr := make(chan error, 1)
	// Recv blocks on the stream, not on ctx: it runs apart so a revocation
	// or the run's end can return at once.
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				if status.Code(err) == codes.ResourceExhausted {
					s.d.counts.Oversize.Add(1)
				}
				recvErr <- err
				return
			}
			select {
			case reports <- req.GetReport():
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case r := <-reports:
			if !a.tok.Usable(time.Now()) {
				return statusExpired
			}
			ack, err := s.handle(ctx, a, from, r)
			if err != nil {
				return err
			}
			if err := stream.Send(&ingestv1.StreamReportsResponse{Ack: ack}); err != nil {
				return err
			}
		}
	}
}

// Publish acks a batch of reports, for senders that do not stream. If the
// call ends partway through (a revocation, an expiry, the run ending), it
// returns an error with no acks even though earlier reports were already
// sunk: the sender's retry relies on the runner's repeat suppression.
func (s *service) Publish(ctx context.Context, req *ingestv1.PublishRequest) (*ingestv1.PublishResponse, error) {
	if n := len(req.GetReports()); n > s.d.maxPublishBatch {
		return nil, status.Errorf(codes.InvalidArgument, "a Publish takes at most %d reports, not %d", s.d.maxPublishBatch, n)
	}
	a := caller(ctx)
	from := peerOf(ctx)
	acks := make([]*ingestv1.Ack, 0, len(req.GetReports()))
	for _, r := range req.GetReports() {
		if !a.tok.Usable(time.Now()) {
			return nil, statusExpired
		}
		ack, err := s.handle(ctx, a, from, r)
		if err != nil {
			return nil, err
		}
		acks = append(acks, ack)
	}
	return &ingestv1.PublishResponse{Acks: acks}, nil
}

// handle decides one report. An error ends the call (the run or the call
// ended while the sink was waiting); everything else is an ack.
func (s *service) handle(ctx context.Context, a authed, from string, r *ingestv1.Report) (*ingestv1.Ack, error) {
	ack := &ingestv1.Ack{ClientRef: r.GetClientRef()}
	// A heartbeat is never rate limited: a refused one would take its
	// emplacement offline and refuse its fixes. A state that is refused is
	// charged by state itself, before it is dead-lettered (see limited).
	_, heartbeat := r.GetBody().(*ingestv1.Report_EmplacementState)
	if !heartbeat && s.limited(from) {
		return s.reject(ack, "rate_limited"), nil
	}
	s.d.senders.Seen(from, time.Now().UTC())
	s.d.counts.Accepted.Add(1)
	// A token below the connection's level could not hold anything it sent,
	// and a dead letter (visible at the connection's level) must not be
	// written for it, whatever the body: no body is read at all.
	if classification.Rank(s.d.floor) > classification.Rank(a.tok.Classification) {
		if heartbeat && s.limited(from) {
			return s.reject(ack, "rate_limited"), nil
		}
		return s.reject(ack, "above_ceiling"), nil
	}
	switch body := r.GetBody().(type) {
	case *ingestv1.Report_Observation:
		return s.observation(ctx, a, from, r, body.Observation, ack)
	case *ingestv1.Report_EmplacementState:
		return s.state(ctx, a, from, r, body.EmplacementState, ack)
	default:
		return s.deadLetter(ack, from, r, "empty_report", errors.New("the report has neither an observation nor an emplacement_state")), nil
	}
}

// limited charges a report to the rate limits and reports whether it is over
// them. The sender's own bucket is drawn on first, so a flooder spends none
// of the shared one.
func (s *service) limited(from string) bool {
	if !s.d.perSender.Allow(transport.SenderIP(from), time.Now()) {
		s.d.senders.RateLimited(from, 1)
		s.d.counts.SenderRateLimited.Add(1)
		return true
	}
	if !s.d.limit.Allow() {
		s.d.counts.RateLimited.Add(1)
		return true
	}
	return false
}

func (s *service) observation(ctx context.Context, a authed, from string, r *ingestv1.Report, m *sentinelv1.Observation, ack *ingestv1.Ack) (*ingestv1.Ack, error) {
	e, bound, reason := s.resolve(a, r.GetEmplacementId())
	if reason != "" {
		return s.reject(ack, reason), nil
	}
	if bound {
		ack.EmplacementId = e.ID
	}
	if m == nil {
		// Dead letters are visible at the connection's level only: an empty
		// report that claims an emplacement above it is refused, not stored.
		if bound && classification.Rank(e.Classification) > classification.Rank(s.d.floor) {
			return s.reject(ack, "empty_report"), nil
		}
		return s.deadLetter(ack, from, r, "empty_report", errors.New("the report's observation is empty")), nil
	}
	// A bound report is taken only while its emplacement heartbeats: a silent
	// sensor's fixes are refused, whatever else is wrong with them. A
	// connection with no emplacements (bound false) is not affected.
	if bound && !s.d.heartbeatsOff {
		if t := s.d.liveness(); t == nil || !t.Online(e.ID, s.d.now().UTC()) {
			return s.reject(ack, "no_heartbeat"), nil
		}
	}
	// The level the runner will stamp: the highest of the feed's, the
	// emplacement's and the report's own. A token may not send above its
	// ceiling. This is decided before Validate, so a report above the token
	// never reaches a dead letter. The emplacement's level can never trigger
	// above_ceiling itself, because resolve already hides emplacements above
	// the token; it is counted here for the dead-letter rule below.
	declared := declaredLevel(m)
	content := classification.Rank(declared)
	if bound {
		content = max(content, classification.Rank(e.Classification))
	}
	if max(classification.Rank(s.d.floor), content) > classification.Rank(a.tok.Classification) {
		return s.reject(ack, "above_ceiling"), nil
	}
	// A polar measurement is checked here, not left to Validate, which would
	// drop a non-finite one silently and let the report through without it.
	if p := m.GetPolar(); p != nil {
		if badPolar(p) {
			return s.reject(ack, "bad_polar"), nil
		}
		if (m.GetPosition() == nil || altitudeOnly(m.GetPosition())) && !bound {
			return s.reject(ack, "unknown_emplacement"), nil
		}
	}
	// Validate a copy without the EMPLACEMENT identities: only an
	// emplacement's own report carries one, and Validate would otherwise key
	// an identity-only target on it (a shared repeat key and observation id).
	// The sender's original bytes stay in Raw.
	mv := proto.Clone(m).(*sentinelv1.Observation)
	mv.Identities = slices.DeleteFunc(mv.Identities, func(id *sentinelv1.Identity) bool {
		return id.GetType() == sentinelv1.IdentityType_IDENTITY_TYPE_EMPLACEMENT
	})
	o, err := sentinelobs.Validate(mv, contentType, "")
	if err != nil {
		reason := decoder.RejectReason(err)
		if reason == "" {
			reason = "bad_message"
		}
		// Dead letters and their cause texts (which can carry the position)
		// are visible at the connection's level only: a report marked above
		// it is refused, not stored.
		if content > classification.Rank(s.d.floor) {
			return s.reject(ack, reason), nil
		}
		return s.deadLetter(ack, from, r, reason, err), nil
	}
	o.Decoder = Decoder
	stripEmplacementIdentities(o) // a backstop: mv carries none
	// An ownship SENSOR observation is keyed on the emplacement's id, so a
	// target with that key would share its repeat key and observation ids.
	if bound && o.SourceEntityID == e.ID {
		return s.reject(ack, "bad_identity"), nil
	}
	o.ReceivedAt = s.d.now().UTC()
	if bound {
		o.EmplacementID, o.SensorID = e.ID, e.ID
		if o.Source == "" {
			o.Source = emplacement.SourceFor(e.SensorType)
		}
		o.Exercise = e.Exercise
		if classification.Rank(e.Classification) > classification.Rank(o.Classification) {
			o.Classification = classification.Normalize(e.Classification)
		}
	} else if o.SensorID == "" {
		o.SensorID = transport.SenderIP(from)
	}
	if bound && (o.Polar != nil || s.d.cfg.EnforceCoverage) {
		at := o.SourceTimestamp
		if at.IsZero() {
			at = o.ReceivedAt
		}
		// A position of exactly 0,0 with an altitude carries only the
		// target's altitude: the polar places it, at that altitude.
		if o.Polar != nil && o.HasPosition && altitudeOnly(m.GetPosition()) {
			o.HasPosition = false
		}
		org, why := s.originOf(e, at)
		if o.Polar != nil && !o.HasPosition {
			if why != "" {
				return s.reject(ack, why), nil
			}
			if o.Polar.DirectionOnly {
				// A detection placed on the terrain goes on as a position
				// does; a bearing is accepted and emits nothing.
				var b *bearing
				var why string
				b, why, org = s.place(ctx, o, e, org)
				switch {
				case b != nil:
					return s.accepted(ctx, ack, e.ID, o.ReceivedAt), nil
				case why != "":
					return s.reject(ack, why), nil
				}
			} else {
				if why := convert(o, e, org); why != "" {
					return s.reject(ack, why), nil
				}
				s.applyRadarTerrain(ctx, o)
			}
		}
		if s.d.cfg.EnforceCoverage && o.HasPosition {
			if why != "" {
				return s.reject(ack, why), nil
			}
			alt := org.at.Alt
			if o.Altitude != nil {
				alt = *o.Altitude
			}
			if !inCoverage(e, org, geo.LLA{Lat: o.Latitude, Lon: o.Longitude, Alt: alt}) {
				return s.reject(ack, "outside_coverage"), nil
			}
		}
	}
	if raw, err := proto.Marshal(m); err == nil {
		o.Raw = raw
	}
	if err := s.sink.Emit(ctx, o); err != nil {
		return nil, err
	}
	empID := ""
	if bound {
		empID = e.ID
	}
	ack = s.accepted(ctx, ack, empID, o.ReceivedAt)
	s.sink.Succeeded(1)
	return ack, nil
}

// accepted acks a report taken, emitted or not (a bearing emits nothing).
// Only an accepted report counts as its emplacement (empID, "" for none)
// reporting.
func (s *service) accepted(ctx context.Context, ack *ingestv1.Ack, empID string, at time.Time) *ingestv1.Ack {
	if empID != "" {
		s.d.touch(ctx, empID, nil, nil, at, at)
	}
	s.d.reportsOK.Add(1)
	ack.Status = ingestv1.AckStatus_ACK_STATUS_OK
	return ack
}

// declaredLevel is the level a report marks itself with, as wire maps it:
// unspecified or unknown is UNCLASSIFIED.
func declaredLevel(m *sentinelv1.Observation) string {
	return classification.Normalize(strings.TrimPrefix(m.GetClassification().String(), "CLASSIFICATION_"))
}

func (s *service) reject(ack *ingestv1.Ack, reason string) *ingestv1.Ack {
	s.d.rejectedMu.Lock()
	s.d.rejected[reason]++
	s.d.rejectedMu.Unlock()
	ack.Status, ack.Reason = ingestv1.AckStatus_ACK_STATUS_REJECTED, reason
	return ack
}

// deadLetter records a report nothing can make an observation of, with its
// sender, and tells the sender why.
func (s *service) deadLetter(ack *ingestv1.Ack, from string, r *ingestv1.Report, reason string, cause error) *ingestv1.Ack {
	raw, _ := proto.Marshal(r)
	now := time.Now().UTC()
	if ps, ok := s.sink.(driver.ProvenanceSink); ok {
		ps.DeadLetterFrom(raw, reason, cause, driver.Provenance{Decoder: Decoder, Sender: from, ReceivedAt: now})
	} else {
		s.sink.DeadLetter(raw, reason, cause)
	}
	s.d.senders.Malformed(from, reason, cause.Error(), now)
	s.d.deadLettered.Add(1)
	ack.Status, ack.Reason = ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED, reason
	return ack
}
