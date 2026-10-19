package grpcin

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel/ingest-service/internal/ingesttoken"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func observation(id string, lat, lon float64) *ingestv1.Report {
	return &ingestv1.Report{ClientRef: id, Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{
		SourceEntityId: id, Position: &sentinelv1.Coordinate{Latitude: lat, Longitude: lon},
	}}}
}

func openStream(t *testing.T, r *rig, secret string) ingestv1.IngestService_StreamReportsClient {
	t.Helper()
	ctx, cancel := withToken(secret)
	t.Cleanup(cancel)
	st, err := r.client(t).StreamReports(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func send(t *testing.T, st ingestv1.IngestService_StreamReportsClient, rep *ingestv1.Report) *ingestv1.Ack {
	t.Helper()
	if err := st.Send(&ingestv1.StreamReportsRequest{Report: rep}); err != nil {
		t.Fatal(err)
	}
	resp, err := st.Recv()
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetAck()
}

func TestStreamAcksEachReport(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	r := start(t, nil, nil, f, nil, nil)
	st := openStream(t, r, secretA)
	for _, tc := range []struct {
		rep    *ingestv1.Report
		status ingestv1.AckStatus
		reason string
	}{
		{observation("EMITTER-0001", 50.1, -4.2), ingestv1.AckStatus_ACK_STATUS_OK, ""},
		{&ingestv1.Report{ClientRef: "no-id", Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{}}}, ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED, "missing_identity"},
		{observation("far", 95, 0), ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED, "bad_position"},
		{observation("nan", math.NaN(), 0), ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED, "bad_position"},
		{&ingestv1.Report{ClientRef: "own", Body: &ingestv1.Report_EmplacementState{EmplacementState: &sentinelv1.EmplacementState{}}}, ingestv1.AckStatus_ACK_STATUS_REJECTED, "unsupported"},
		{&ingestv1.Report{ClientRef: "empty"}, ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED, "empty_report"},
		{observation("EMITTER-0002", 50.2, -4.3), ingestv1.AckStatus_ACK_STATUS_OK, ""},
	} {
		ack := send(t, st, tc.rep)
		if ack.GetClientRef() != tc.rep.GetClientRef() || ack.GetStatus() != tc.status || ack.GetReason() != tc.reason {
			t.Errorf("ack for %q = %v, want %v %q", tc.rep.GetClientRef(), ack, tc.status, tc.reason)
		}
	}
	emitted := r.sink.emitted()
	if len(emitted) != 2 {
		t.Fatalf("emitted %d observations, want 2", len(emitted))
	}
	o := emitted[0]
	if o.Decoder != Decoder || o.SourceEntityID != "EMITTER-0001" || o.SensorID == "" || len(o.Raw) == 0 || o.RawContentType != "application/x-protobuf" || o.ReceivedAt.IsZero() {
		t.Errorf("observation = %+v", o)
	}
	letters := r.sink.deadLetters()
	if len(letters) != 4 {
		t.Fatalf("dead letters = %d, want 4", len(letters))
	}
	if letters[0].reason != "missing_identity" || letters[0].p.Decoder != Decoder || letters[0].p.Sender == "" {
		t.Errorf("dead letter = %+v", letters[0])
	}
	h := r.d.Health().Detail
	if h["reportsOk"] != int64(2) || h["deadLettered"] != int64(4) || h["reportsRejected"].(map[string]int64)["unsupported"] != 1 || h["accepted"] != int64(7) {
		t.Errorf("health = ok %v dead %v rejected %v accepted %v", h["reportsOk"], h["deadLettered"], h["reportsRejected"], h["accepted"])
	}
}

func TestAboveTheTokensCeilingIsRejected(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"})
	r := start(t, nil, nil, f, nil, nil)
	st := openStream(t, r, secretA)
	secret := observation("s", 1, 1)
	secret.GetObservation().Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	if ack := send(t, st, secret); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_REJECTED || ack.GetReason() != "above_ceiling" {
		t.Errorf("a SECRET report on a CONFIDENTIAL token: %v", ack)
	}
	if ack := send(t, st, observation("c", 1, 1)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Errorf("an unmarked report: %v", ack)
	}
}

// The feed's own level is a floor every report is raised to, so a token
// below it can send nothing.
func TestAFeedAboveTheTokenRejectsEverything(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "UNCLASSIFIED"})
	r := start(t, map[string]any{"classification": "SECRET"}, nil, f, nil, nil)
	st := openStream(t, r, secretA)
	if ack := send(t, st, observation("x", 1, 1)); ack.GetReason() != "above_ceiling" {
		t.Errorf("ack = %v, want above_ceiling", ack)
	}
}

func TestRateLimits(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	r := start(t, map[string]any{"maxMessagesPerSec": 1}, nil, f, nil, nil)
	st := openStream(t, r, secretA)
	send(t, st, observation("a", 1, 1))
	if ack := send(t, st, observation("b", 1, 1)); ack.GetReason() != "rate_limited" {
		t.Errorf("second report in the same second: %v", ack)
	}
	r2 := start(t, map[string]any{"maxMessagesPerSecPerSender": 1}, nil, f, nil, nil)
	st2 := openStream(t, r2, secretA)
	send(t, st2, observation("a", 1, 1))
	if ack := send(t, st2, observation("b", 1, 1)); ack.GetReason() != "rate_limited" {
		t.Errorf("second report from one sender: %v", ack)
	}
	if r2.d.counts.SenderRateLimited.Load() != 1 {
		t.Errorf("senderRateLimited = %d, want 1", r2.d.counts.SenderRateLimited.Load())
	}
}

func TestPublish(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	r := start(t, map[string]any{"maxPublishBatch": 2}, nil, f, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	resp, err := r.client(t).Publish(ctx, &ingestv1.PublishRequest{Reports: []*ingestv1.Report{observation("a", 1, 1), {ClientRef: "e"}}})
	if err != nil {
		t.Fatal(err)
	}
	if acks := resp.GetAcks(); len(acks) != 2 || acks[0].GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK || acks[1].GetReason() != "empty_report" {
		t.Errorf("acks = %v", acks)
	}
	_, err = r.client(t).Publish(ctx, &ingestv1.PublishRequest{Reports: []*ingestv1.Report{observation("a", 1, 1), observation("b", 1, 1), observation("c", 1, 1)}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("an oversized batch = %v, want InvalidArgument", err)
	}
}

// An ack waits for the sink: a saturated publisher slows the sender.
func TestAckWaitsForTheSink(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	sink := &memSink{block: make(chan struct{})}
	r := start(t, nil, nil, f, nil, sink)
	st := openStream(t, r, secretA)
	if err := st.Send(&ingestv1.StreamReportsRequest{Report: observation("a", 1, 1)}); err != nil {
		t.Fatal(err)
	}
	got := make(chan *ingestv1.Ack, 1)
	go func() {
		if resp, err := st.Recv(); err == nil {
			got <- resp.GetAck()
		}
	}()
	select {
	case ack := <-got:
		t.Fatalf("acked before the sink took it: %v", ack)
	case <-time.After(200 * time.Millisecond):
	}
	close(sink.block)
	select {
	case ack := <-got:
		if ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
			t.Errorf("ack = %v", ack)
		}
	case <-time.After(waitTimeout):
		t.Fatal("no ack after the sink took the report")
	}
}

func TestRevokeEndsOnlyThatTokensStreams(t *testing.T) {
	const secretB = "si_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	f := newFakeTokens()
	f.add(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	f.add(secretB, ingesttoken.Token{ID: "t2", FeedID: "grpc-1"})
	r := start(t, nil, nil, f, nil, nil)
	a := openStream(t, r, secretA)
	b := openStream(t, r, secretB)
	send(t, a, observation("a", 1, 1))
	send(t, b, observation("b", 1, 1))
	f.revoke(secretA)
	if _, err := a.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("the revoked token's stream ended with %v, want Unauthenticated", err)
	}
	if ack := send(t, b, observation("b2", 1, 1)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Errorf("the other token's stream: %v", ack)
	}
}

func TestExpiredTokenEndsTheStream(t *testing.T) {
	soon := time.Now().Add(300 * time.Millisecond)
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1", ExpiresAt: &soon})
	r := start(t, nil, nil, f, nil, nil)
	st := openStream(t, r, secretA)
	send(t, st, observation("a", 1, 1))
	time.Sleep(time.Until(soon) + 50*time.Millisecond)
	if err := st.Send(&ingestv1.StreamReportsRequest{Report: observation("b", 1, 1)}); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if _, err := st.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("after expiry the stream ended with %v, want Unauthenticated", err)
	}
}

// An idle stream ends at the token's expiry: the token cache announces
// revocations but not expiries, so the call carries the deadline itself.
func TestExpiryEndsAnIdleStream(t *testing.T) {
	soon := time.Now().Add(300 * time.Millisecond)
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1", ExpiresAt: &soon})
	r := start(t, nil, nil, f, nil, nil)
	st := openStream(t, r, secretA)
	got := make(chan error, 1)
	go func() { _, err := st.Recv(); got <- err }()
	select {
	case err := <-got:
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("an idle stream at expiry ended with %v, want Unauthenticated", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an idle stream outlived its token's expiry")
	}
}

func TestEnded(t *testing.T) {
	cut := func(cause error) context.Context {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)
		return ctx
	}
	handlerErr := errors.New("handler failed")
	live, stop := context.WithCancel(context.Background())
	defer stop()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want codes.Code
		same bool
	}{
		{"nil stays nil", cut(errRevoked), nil, codes.OK, true},
		{"a live call passes the error through", live, handlerErr, codes.Unknown, true},
		{"revoked", cut(errRevoked), handlerErr, codes.Unauthenticated, false},
		{"expired", cut(errExpired), handlerErr, codes.Unauthenticated, false},
		{"moving", cut(errMoving), handlerErr, codes.Unavailable, false},
		{"a client cancel passes the error through", cut(context.Canceled), handlerErr, codes.Unknown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ended(tc.ctx, tc.err)
			if tc.same && got != tc.err {
				t.Fatalf("ended = %v, want %v unchanged", got, tc.err)
			}
			if status.Code(got) != tc.want {
				t.Fatalf("code = %v, want %v", status.Code(got), tc.want)
			}
		})
	}
}

// The feed stopping (a lease move) ends open streams with Unavailable, even
// one blocked in a saturated sink, and Run returns within the bound.
func TestStopEndsStreamsWithUnavailable(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	sink := &memSink{block: make(chan struct{})} // never released
	r := start(t, nil, nil, f, nil, sink)
	st := openStream(t, r, secretA)
	if err := st.Send(&ingestv1.StreamReportsRequest{Report: observation("a", 1, 1)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the report is now inside Emit
	began := time.Now()
	if err := r.stop(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if took := time.Since(began); took > stopGrace+1500*time.Millisecond {
		t.Errorf("stop took %v", took)
	}
	if _, err := st.Recv(); status.Code(err) != codes.Unavailable {
		t.Fatalf("an open stream ended with %v, want Unavailable", err)
	}
}

func TestOversizeReportIsCounted(t *testing.T) {
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"})
	r := start(t, map[string]any{"maxMessageBytes": 1024}, nil, f, nil, nil)
	st := openStream(t, r, secretA)
	big := observation("big", 1, 1)
	big.GetObservation().DisplayName = string(make([]byte, 4096))
	_ = st.Send(&ingestv1.StreamReportsRequest{Report: big})
	if _, err := st.Recv(); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("an oversized report ended the stream with %v, want ResourceExhausted", err)
	}
	waitFor(t, func() bool { return r.d.counts.Oversize.Load() == 1 })
}
