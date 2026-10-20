package grpcin

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

const secretA = "si_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func withToken(secret string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+secret), cancel
}

func tokensWith(secret string, t ingesttoken.Token) *fakeTokens {
	f := newFakeTokens()
	f.add(secret, t)
	return f
}

func TestNotReadyIsUnavailable(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	f.ready = make(chan struct{}) // never loaded
	r := start(t, nil, nil, f, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	_, err := r.client(t).GetSession(ctx, &ingestv1.GetSessionRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("GetSession before the tokens load = %v, want Unavailable", err)
	}
}

func TestEmplacementsNotReadyIsUnavailable(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	r := start(t, nil, nil, f, &fakeEmplacements{notReady: true}, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	_, err := r.client(t).GetSession(ctx, &ingestv1.GetSessionRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("GetSession before the emplacements load = %v, want Unavailable", err)
	}
}

func TestAuthentication(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	f := newFakeTokens()
	f.add(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	f.add("si_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ingesttoken.Token{ID: "t2", FeedID: "other-feed"})
	f.add("si_ccccccccccccccccccccccccccccccccccccccccccc", ingesttoken.Token{ID: "t3", FeedID: "grpc-1", RevokedAt: &past})
	f.add("si_ddddddddddddddddddddddddddddddddddddddddddd", ingesttoken.Token{ID: "t4", FeedID: "grpc-1", ExpiresAt: &past})
	r := start(t, nil, nil, f, nil, nil)
	c := r.client(t)
	for _, tc := range []struct {
		name, header string
		want         codes.Code
	}{
		{"no token", "", codes.Unauthenticated},
		{"not a bearer", "Basic abc", codes.Unauthenticated},
		{"unknown", "Bearer si_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", codes.Unauthenticated},
		{"revoked", "Bearer si_ccccccccccccccccccccccccccccccccccccccccccc", codes.Unauthenticated},
		{"expired", "Bearer si_ddddddddddddddddddddddddddddddddddddddddddd", codes.Unauthenticated},
		{"another feed's", "Bearer si_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", codes.PermissionDenied},
		{"good, lower-case scheme", "bearer " + secretA, codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
			defer cancel()
			if tc.header != "" {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", tc.header)
			}
			_, err := c.GetSession(ctx, &ingestv1.GetSessionRequest{})
			if status.Code(err) != tc.want {
				t.Fatalf("GetSession = %v, want %v", err, tc.want)
			}
		})
	}
}

// A token revoked between the check and the call's registration is caught
// by the second check.
func TestRevokedBetweenTheChecksIsRefused(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	f.failAfter = 2 // the first Authenticate succeeds, the re-check fails
	r := start(t, nil, nil, f, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	_, err := r.client(t).GetSession(ctx, &ingestv1.GetSessionRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("GetSession = %v, want Unauthenticated", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestGetSessionAndListEmplacements(t *testing.T) {
	feed := "grpc-1"
	other := "grpc-2"
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{
		{ID: "e-low", Name: "Coastal ESM", Mobility: "FIXED", SensorType: "RF", Position: &emplacement.Point{Lat: 50.1, Lon: -4.2}, Altitude: ptr(12.0),
			BoresightDeg: 200, AzimuthReference: "TRUE_NORTH", Coverage: emplacement.Coverage{MaxRangeM: 40000, AzimuthWidthDeg: 120},
			FeedID: &feed, Classification: "UNCLASSIFIED", Active: true, UpdatedAt: time.Unix(1700000000, 0).UTC()},
		{ID: "e-secret", Name: "Secret site", Mobility: "FIXED", SensorType: "RADAR", FeedID: &feed, Classification: "SECRET", Active: true},
		{ID: "e-other", Name: "Elsewhere", Mobility: "MOBILE", SensorType: "RF", FeedID: &other, Classification: "UNCLASSIFIED", Active: true},
	}}
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: feed, Classification: "CONFIDENTIAL"})
	r := start(t, map[string]any{"classification": "CONFIDENTIAL", "senderManagedEmplacements": true, "maxMessagesPerSec": 50}, nil, f, emps, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	s, err := r.client(t).GetSession(ctx, &ingestv1.GetSessionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if s.GetFeedId() != feed || s.GetClassificationFloor() != sentinelv1.Classification_CLASSIFICATION_CONFIDENTIAL ||
		!s.GetSenderManagedEmplacements() || s.GetMaxMessageBytes() != 65536 || s.GetMaxMessagesPerSec() != 50 {
		t.Errorf("session = %v", s)
	}
	// Only this feed's emplacements, and none above the token's ceiling.
	if n := len(s.GetEmplacements()); n != 1 {
		t.Fatalf("session lists %d emplacements, want 1 (e-low): %v", n, s.GetEmplacements())
	}
	e := s.GetEmplacements()[0]
	if e.GetId() != "e-low" || e.GetMobility() != sentinelv1.Mobility_MOBILITY_FIXED || e.GetSensorType() != sentinelv1.SensorType_SENSOR_TYPE_RF ||
		e.GetPosition().GetLatitude() != 50.1 || e.GetPosition().GetAltitudeMeters() != 12 || e.GetBoresightDeg() != 200 ||
		e.GetAzimuthReference() != sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH || e.GetCoverage().GetMaxRangeM() != 40000 ||
		e.GetFeedId() != feed || !e.GetActive() || e.GetUpdatedAt().AsTime().Unix() != 1700000000 {
		t.Errorf("emplacement = %v", e)
	}
	l, err := r.client(t).ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
	if err != nil || len(l.GetEmplacements()) != 1 || l.GetEmplacements()[0].GetId() != "e-low" {
		t.Fatalf("ListEmplacements = %v, %v", l, err)
	}
}

// The registry cancels a token's calls, and only that token's.
func TestCallsRevokeOnlyThatToken(t *testing.T) {
	var c calls
	a, cancelA := context.WithCancelCause(context.Background())
	b, cancelB := context.WithCancelCause(context.Background())
	removeA := c.add("t1", cancelA)
	defer removeA()
	defer c.add("t2", cancelB)()
	c.revoke("t1")
	if context.Cause(a) != errRevoked {
		t.Errorf("t1's call cause = %v, want errRevoked", context.Cause(a))
	}
	if b.Err() != nil {
		t.Error("t2's call ended too")
	}
}
