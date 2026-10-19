package grpcin

import (
	"context"
	"errors"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/classification"
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

// Publish acks a batch of reports, for senders that do not stream.
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
	// The sender's own bucket first, so a flooder spends none of the shared one.
	if !s.d.perSender.Allow(transport.SenderIP(from), time.Now()) {
		s.d.senders.RateLimited(from, 1)
		s.d.counts.SenderRateLimited.Add(1)
		return s.reject(ack, "rate_limited"), nil
	}
	if !s.d.limit.Allow() {
		s.d.counts.RateLimited.Add(1)
		return s.reject(ack, "rate_limited"), nil
	}
	s.d.senders.Seen(from, time.Now().UTC())
	s.d.counts.Accepted.Add(1)
	switch body := r.GetBody().(type) {
	case *ingestv1.Report_Observation:
		return s.observation(ctx, a, from, r, body.Observation, ack)
	case *ingestv1.Report_EmplacementState:
		// An emplacement's own position: C2 handles it.
		return s.reject(ack, "unsupported"), nil
	default:
		return s.deadLetter(ack, from, r, "empty_report", errors.New("the report has neither an observation nor an emplacement_state")), nil
	}
}

func (s *service) observation(ctx context.Context, a authed, from string, r *ingestv1.Report, m *sentinelv1.Observation, ack *ingestv1.Ack) (*ingestv1.Ack, error) {
	o, err := sentinelobs.Validate(m, contentType, "")
	if err != nil {
		reason := decoder.RejectReason(err)
		if reason == "" {
			reason = "bad_message"
		}
		return s.deadLetter(ack, from, r, reason, err), nil
	}
	// The level the runner will stamp: the feed's, or the report's when it is
	// higher. A token may not send above its ceiling.
	level := max(classification.Rank(s.d.floor), classification.Rank(o.Classification))
	if level > classification.Rank(a.tok.Classification) {
		return s.reject(ack, "above_ceiling"), nil
	}
	o.Decoder = Decoder
	if o.SensorID == "" {
		o.SensorID = from
	}
	o.ReceivedAt = time.Now().UTC()
	if raw, err := proto.Marshal(m); err == nil {
		o.Raw = raw
	}
	if err := s.sink.Emit(ctx, o); err != nil {
		return nil, err
	}
	s.d.reportsOK.Add(1)
	s.sink.Succeeded(1)
	ack.Status = ingestv1.AckStatus_ACK_STATUS_OK
	return ack, nil
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
