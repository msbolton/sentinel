package grpcin

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/source/transport"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// cacheWait is how long an upsert waits for the emplacement cache to see its
// write, so the sender's next report can claim the emplacement.
const cacheWait = 2 * time.Second

// UpsertEmplacement creates or updates one of the emplacements this
// connection's senders manage. An id names one to update: it must be bound
// to this connection and made by one of its senders, else NotFound. An empty
// or unknown id looks for a sender's row of the same name (ignoring case and
// surrounding space) on this connection, so a retried upsert is idempotent,
// and updates it; failing that it creates one, up to maxEmplacements. A name
// any other row holds is AlreadyExists. The connection, the creator and
// whether it is active are Sentinel's to set, not the sender's. Upserts draw
// on the same rate limits as reports.
func (s *service) UpsertEmplacement(ctx context.Context, req *ingestv1.UpsertEmplacementRequest) (*ingestv1.UpsertEmplacementResponse, error) {
	if !s.d.cfg.SenderManagedEmplacements {
		return nil, status.Error(codes.PermissionDenied, "this connection does not let senders manage their emplacements")
	}
	if s.d.writer == nil {
		return nil, status.Error(codes.Unavailable, "this replica cannot write emplacements: retry")
	}
	a := caller(ctx)
	from := peerOf(ctx)
	// The sender's own bucket first, so a flooder spends none of the shared one.
	if !s.d.perSender.Allow(transport.SenderIP(from), time.Now()) {
		s.d.senders.RateLimited(from, 1)
		s.d.counts.SenderRateLimited.Add(1)
		return nil, status.Error(codes.ResourceExhausted, "rate limited")
	}
	if !s.d.limit.Allow() {
		s.d.counts.RateLimited.Add(1)
		return nil, status.Error(codes.ResourceExhausted, "rate limited")
	}
	feed := s.d.feedID
	ceiling := classification.Rank(a.tok.Classification)
	// What a sender writes was authored over this connection: a token below
	// the connection's level could not hold it.
	if classification.Rank(s.d.floor) > ceiling {
		return nil, status.Error(codes.PermissionDenied, "this connection is above the token's classification")
	}
	m := req.GetEmplacement()
	if m == nil {
		return nil, status.Error(codes.InvalidArgument, "send an emplacement")
	}
	in := inputOf(m)
	if s.d.cfg.Exercise {
		// An exercise connection's emplacements are exercise: the row matches
		// the SENSOR entity the connection reports for it.
		on := true
		in.Exercise = &on
	}
	own := func(context.Context, string) (emplacement.FeedInfo, error) { // the connection itself
		return emplacement.FeedInfo{Exists: true, Inbound: true, Driver: ID}, nil
	}
	check := func(e *emplacement.Emplacement) error {
		e.FeedID = &feed
		// Never below the connection's own level.
		if classification.Rank(e.Classification) < classification.Rank(s.d.floor) {
			e.Classification = classification.Normalize(s.d.floor)
		}
		return emplacement.CheckWrite(ctx, e, ceiling, own)
	}
	// mine is whether the senders may touch e: bound to this connection, made
	// by a sender, and not above this token's ceiling.
	mine := func(e emplacement.Emplacement) bool {
		return e.FeedID != nil && *e.FeedID == feed && e.SenderManaged() && classification.Rank(e.Classification) <= ceiling
	}

	// target is the row to update: the one the id names, else the sender's
	// own row of the same name.
	var target *emplacement.Emplacement
	if m.GetId() != "" {
		existing, err := s.d.writer.Get(ctx, m.GetId())
		switch {
		case err == nil:
			if !mine(existing) {
				return nil, errNotYours
			}
			target = &existing
		case !errors.Is(err, emplacement.ErrNotFound):
			return nil, s.failed(err)
		}
	}
	var rows []emplacement.Emplacement
	if target == nil {
		var err error
		if rows, err = s.d.writer.List(ctx, emplacement.Filter{FeedID: feed}); err != nil {
			return nil, s.failed(err)
		}
		name := strings.TrimSpace(m.GetName())
		for _, r := range rows {
			if mine(r) && strings.EqualFold(strings.TrimSpace(r.Name), name) {
				target = &r
				break
			}
		}
	}

	var saved emplacement.Emplacement
	var err error
	if target != nil {
		saved, err = s.d.writer.Update(ctx, target.ID, 0, func(e *emplacement.Emplacement) error {
			if !mine(*e) { // it changed since it was read
				return emplacement.ErrNotFound
			}
			active, exercise := e.Active, e.Exercise
			in.Apply(e)
			e.Active = active
			// A sender may set the exercise flag but never clear it: a
			// person does, over REST.
			e.Exercise = exercise || e.Exercise
			if p := m.GetPosition(); p != nil && p.AltitudeMeters == nil {
				e.Altitude = nil // a position sent without an altitude has none
			}
			return check(e)
		})
	} else {
		n := 0
		for _, r := range rows {
			if r.SenderManaged() {
				n++
			}
		}
		if n >= s.d.maxEmplacements {
			return nil, status.Error(codes.ResourceExhausted, "this connection's senders manage as many emplacements as it allows")
		}
		e := emplacement.New()
		in.Apply(&e)
		e.Active = true
		e.CreatedBy = emplacement.SenderPrefix + a.tok.ID
		if err = check(&e); err == nil {
			saved, err = s.d.writer.Create(ctx, e)
		}
	}
	if err != nil {
		return nil, s.failed(err)
	}
	if !s.awaitCache(ctx, saved.ID, saved.Version) {
		return nil, status.Error(codes.Unavailable, "the emplacement was saved; retry once the service has caught up")
	}
	return &ingestv1.UpsertEmplacementResponse{Emplacement: emplacementProto(saved)}, nil
}

var errNotYours = status.Error(codes.NotFound, "no emplacement of this connection's senders has that id")

// failed turns a store or validation error into the sender's status.
func (s *service) failed(err error) error {
	var ve *emplacement.ValidationError
	switch {
	case errors.As(err, &ve):
		parts := make([]string, 0, len(ve.Problems))
		for _, p := range ve.Problems {
			parts = append(parts, p.Path+" "+p.Message)
		}
		return status.Error(codes.InvalidArgument, strings.Join(parts, "; "))
	case errors.Is(err, emplacement.ErrNameTaken):
		return status.Error(codes.AlreadyExists, "an emplacement already has that name")
	case errors.Is(err, emplacement.ErrNotFound):
		return errNotYours
	}
	s.d.logger.Warn("upserting an emplacement failed", zap.Error(err))
	return status.Error(codes.Internal, "the emplacement could not be saved")
}

// awaitCache waits, briefly, for the emplacement cache to hold the write,
// and reports whether it did. The write itself has succeeded either way.
func (s *service) awaitCache(ctx context.Context, id string, version int) bool {
	seen := func() bool {
		e, ok := s.d.emps.Get(id)
		return ok && e.Version >= version
	}
	if seen() {
		return true
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	timeout := time.NewTimer(s.d.cacheWait)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timeout.C:
			s.d.logger.Debug("the emplacement cache did not see an upsert in time", zap.String("emplacement", id))
			return false
		case <-tick.C:
			if seen() {
				return true
			}
		}
	}
}

// inputOf reads an emplacement message as a write. On every upsert the
// message replaces the name, description and boresight, so a zero value
// there clears the stored one. The exercise flag is read as requested, but
// the caller makes it sticky on an update: a sender can set it and never
// clear it (a person clears it over REST). An unset enum (mobility, sensor
// type, azimuth reference, classification) and an absent position, coverage
// or default sigma keep what is stored, or take the defaults on create. A
// position sent without an altitude clears the stored altitude (the caller
// does that). The mounting height is set when the message carries one and
// kept when it does not. The connection, creator and active flag are never read.
func inputOf(m *sentinelv1.Emplacement) emplacement.Input {
	name, desc := m.GetName(), m.GetDescription()
	in := emplacement.Input{Name: &name, Description: &desc}
	bore := m.GetBoresightDeg()
	in.BoresightDeg = &bore
	exercise := m.GetExercise()
	in.Exercise = &exercise
	enum := func(s, prefix string) *string {
		v := strings.TrimPrefix(s, prefix)
		if v == "UNSPECIFIED" || v == "" {
			return nil
		}
		return &v
	}
	in.Mobility = enum(m.GetMobility().String(), "MOBILITY_")
	in.SensorType = enum(m.GetSensorType().String(), "SENSOR_TYPE_")
	in.AzimuthReference = enum(m.GetAzimuthReference().String(), "AZIMUTH_REFERENCE_")
	in.Classification = enum(m.GetClassification().String(), "CLASSIFICATION_")
	if p := m.GetPosition(); p != nil {
		in.Position = &emplacement.Point{Lat: p.GetLatitude(), Lon: p.GetLongitude()}
		in.Altitude = p.AltitudeMeters
	}
	in.HeightAboveGroundM = m.HeightAboveGroundM // unset keeps the stored height
	if c := m.GetCoverage(); c != nil {
		in.Coverage = &emplacement.Coverage{MinRangeM: c.GetMinRangeM(), MaxRangeM: c.GetMaxRangeM(), AzimuthWidthDeg: c.GetAzimuthWidthDeg(),
			ElevationMinDeg: c.ElevationMinDeg, ElevationMaxDeg: c.ElevationMaxDeg}
	}
	if sg := m.GetDefaultSigma(); sg != nil {
		in.DefaultSigma = &emplacement.Sigma{RangeM: sg.RangeM, AzimuthDeg: sg.AzimuthDeg, ElevationDeg: sg.ElevationDeg}
	}
	return in
}
