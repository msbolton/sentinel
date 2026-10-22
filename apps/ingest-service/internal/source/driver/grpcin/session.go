package grpcin

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/ingest-service/internal/emplacement"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func (s *service) GetSession(ctx context.Context, _ *ingestv1.GetSessionRequest) (*ingestv1.GetSessionResponse, error) {
	return &ingestv1.GetSessionResponse{
		FeedId:                    s.d.feedID,
		Emplacements:              s.emplacements(caller(ctx)),
		ClassificationFloor:       classificationOf(s.d.floor),
		SenderManagedEmplacements: s.d.cfg.SenderManagedEmplacements,
		MaxMessageBytes:           uint32(s.d.maxMessageBytes),
		MaxMessagesPerSec:         uint32(s.d.maxPerSec),
		Exercise:                  s.d.cfg.Exercise,
	}, nil
}

func (s *service) ListEmplacements(ctx context.Context, _ *ingestv1.ListEmplacementsRequest) (*ingestv1.ListEmplacementsResponse, error) {
	return &ingestv1.ListEmplacementsResponse{Emplacements: s.emplacements(caller(ctx))}, nil
}

// emplacements lists the feed's emplacements the token may see: none above
// its classification ceiling. On an exercise connection they all say exercise.
func (s *service) emplacements(a authed) []*sentinelv1.Emplacement {
	ceiling := classification.Rank(a.tok.Classification)
	var out []*sentinelv1.Emplacement
	for _, e := range s.d.emps.ByFeed(s.d.feedID) {
		if classification.Rank(e.Classification) <= ceiling {
			m := emplacementProto(e)
			// The connection's exercise flag is ORed onto every report it makes,
			// its emplacements' SENSOR entities included, whatever an operator
			// left on the row: say so to the sender.
			m.Exercise = m.Exercise || s.d.cfg.Exercise
			out = append(out, m)
		}
	}
	return out
}

func classificationOf(level string) sentinelv1.Classification {
	return sentinelv1.Classification(sentinelv1.Classification_value["CLASSIFICATION_"+classification.Normalize(level)])
}

func emplacementProto(e emplacement.Emplacement) *sentinelv1.Emplacement {
	m := &sentinelv1.Emplacement{
		Id: e.ID, Name: e.Name, Description: e.Description,
		Mobility:         sentinelv1.Mobility(sentinelv1.Mobility_value["MOBILITY_"+e.Mobility]),
		SensorType:       sentinelv1.SensorType(sentinelv1.SensorType_value["SENSOR_TYPE_"+e.SensorType]),
		BoresightDeg:     e.BoresightDeg,
		AzimuthReference: sentinelv1.AzimuthReference(sentinelv1.AzimuthReference_value["AZIMUTH_REFERENCE_"+e.AzimuthReference]),
		Coverage: &sentinelv1.Coverage{
			MinRangeM: e.Coverage.MinRangeM, MaxRangeM: e.Coverage.MaxRangeM, AzimuthWidthDeg: e.Coverage.AzimuthWidthDeg,
			ElevationMinDeg: e.Coverage.ElevationMinDeg, ElevationMaxDeg: e.Coverage.ElevationMaxDeg,
		},
		DefaultSigma:   &sentinelv1.PolarSigma{RangeM: e.DefaultSigma.RangeM, AzimuthDeg: e.DefaultSigma.AzimuthDeg, ElevationDeg: e.DefaultSigma.ElevationDeg},
		Classification: classificationOf(e.Classification),
		Exercise:       e.Exercise,
		Active:         e.Active,

		HeightAboveGroundM: e.HeightAboveGroundM,
	}
	if e.Position != nil {
		m.Position = &sentinelv1.Coordinate{Latitude: e.Position.Lat, Longitude: e.Position.Lon, AltitudeMeters: e.Altitude}
	}
	if e.FeedID != nil {
		m.FeedId = *e.FeedID
	}
	if !e.UpdatedAt.IsZero() {
		m.UpdatedAt = timestamppb.New(e.UpdatedAt)
	}
	return m
}
