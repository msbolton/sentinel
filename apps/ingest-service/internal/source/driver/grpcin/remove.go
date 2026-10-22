package grpcin

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
)

// RemoveEmplacement soft-deletes one of the emplacements this connection's
// senders manage. The id must be bound to this connection and made by one of
// its senders, else NotFound: an unknown id, another connection's row and an
// operator's row look the same to the sender. A second remove finds nothing
// and is NotFound too, which a caller may ignore. Removes draw on the same
// rate limits as reports.
func (s *service) RemoveEmplacement(ctx context.Context, req *ingestv1.RemoveEmplacementRequest) (*ingestv1.RemoveEmplacementResponse, error) {
	ceiling, err := s.admit(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "name an emplacement to remove")
	}
	existing, err := s.d.writer.Get(ctx, req.GetId())
	if err != nil {
		return nil, s.failed(err)
	}
	if !s.mine(existing, ceiling) {
		return nil, errNotYours
	}
	if err := s.d.writer.Delete(ctx, existing.ID); err != nil {
		return nil, s.failed(err)
	}
	return &ingestv1.RemoveEmplacementResponse{}, nil
}
