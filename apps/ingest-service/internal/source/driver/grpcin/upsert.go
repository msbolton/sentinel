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
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// cacheWait is how long an upsert waits for the emplacement cache to see its
// write, so the sender's next report can claim the emplacement.
const cacheWait = 2 * time.Second

// UpsertEmplacement creates or updates one of the emplacements this
// connection's senders manage. An id names one to update: it must be bound
// to this connection and made by one of its senders, else NotFound. An empty
// or unknown id creates one, up to maxEmplacements. The connection, the
// creator and whether it is active are Sentinel's to set, not the sender's.
func (s *service) UpsertEmplacement(ctx context.Context, req *ingestv1.UpsertEmplacementRequest) (*ingestv1.UpsertEmplacementResponse, error) {
	if !s.d.cfg.SenderManagedEmplacements {
		return nil, status.Error(codes.PermissionDenied, "this connection does not let senders manage their emplacements")
	}
	if s.d.writer == nil {
		return nil, status.Error(codes.Unavailable, "this replica cannot write emplacements: retry")
	}
	m := req.GetEmplacement()
	if m == nil {
		return nil, status.Error(codes.InvalidArgument, "send an emplacement")
	}
	a := caller(ctx)
	feed := s.d.feedID
	ceiling := classification.Rank(a.tok.Classification)
	in := inputOf(m)
	own := func(context.Context, string) (bool, bool, error) { return true, true, nil } // the connection itself
	check := func(e *emplacement.Emplacement) error {
		e.FeedID = &feed
		return emplacement.CheckWrite(ctx, e, ceiling, own)
	}
	// mine is whether the senders may touch e: bound to this connection, made
	// by a sender, and not above this token's ceiling.
	mine := func(e emplacement.Emplacement) bool {
		return e.FeedID != nil && *e.FeedID == feed && e.SenderManaged() && classification.Rank(e.Classification) <= ceiling
	}

	var saved emplacement.Emplacement
	var err error
	existing, getErr := s.d.writer.Get(ctx, m.GetId())
	switch {
	case m.GetId() != "" && getErr == nil:
		if !mine(existing) {
			return nil, errNotYours
		}
		saved, err = s.d.writer.Update(ctx, existing.ID, 0, func(e *emplacement.Emplacement) error {
			if !mine(*e) { // it changed since it was read
				return emplacement.ErrNotFound
			}
			active := e.Active
			in.Apply(e)
			e.Active = active
			return check(e)
		})
	case m.GetId() == "" || errors.Is(getErr, emplacement.ErrNotFound):
		rows, lerr := s.d.writer.List(ctx, emplacement.Filter{FeedID: feed})
		if lerr != nil {
			return nil, s.failed(lerr)
		}
		n := 0
		for _, r := range rows {
			if r.SenderManaged() {
				n++
			}
		}
		if n >= s.d.maxEmplacements {
			return nil, status.Errorf(codes.ResourceExhausted, "this connection's senders already manage %d emplacements, its most", n)
		}
		e := emplacement.New()
		in.Apply(&e)
		e.Active = true
		e.CreatedBy = emplacement.SenderPrefix + a.tok.ID
		if err = check(&e); err == nil {
			saved, err = s.d.writer.Create(ctx, e)
		}
	default:
		return nil, s.failed(getErr)
	}
	if err != nil {
		return nil, s.failed(err)
	}
	s.awaitCache(ctx, saved.ID, saved.Version)
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

// awaitCache waits, briefly, for the emplacement cache to hold the write.
func (s *service) awaitCache(ctx context.Context, id string, version int) {
	deadline := time.Now().Add(cacheWait)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if e, ok := s.d.emps.Get(id); ok && e.Version >= version {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// inputOf reads an emplacement message as a write. Unset enums leave the
// field as it is.
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
	if c := m.GetCoverage(); c != nil {
		in.Coverage = &emplacement.Coverage{MinRangeM: c.GetMinRangeM(), MaxRangeM: c.GetMaxRangeM(), AzimuthWidthDeg: c.GetAzimuthWidthDeg(),
			ElevationMinDeg: c.ElevationMinDeg, ElevationMaxDeg: c.ElevationMaxDeg}
	}
	if sg := m.GetDefaultSigma(); sg != nil {
		in.DefaultSigma = &emplacement.Sigma{RangeM: sg.RangeM, AzimuthDeg: sg.AzimuthDeg, ElevationDeg: sg.ElevationDeg}
	}
	return in
}
