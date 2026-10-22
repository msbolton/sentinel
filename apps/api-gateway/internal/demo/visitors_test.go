package demo

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

const (
	visitorTestToken = "si_visitor_test_token"
	visitorWait      = 5 * time.Second
	testTick         = 20 * time.Millisecond
)

// fakeIngest is the ingest service's grpc side as the visitors client sees
// it. Every call checks the bearer token.
type fakeIngest struct {
	ingestv1.UnimplementedIngestServiceServer

	mu      sync.Mutex
	rows    map[string]*sentinelv1.Emplacement // by id
	upserts []*sentinelv1.Emplacement
	removes []string
	lists   int
	states  []*sentinelv1.EmplacementState
	refs    []string
	streams int
	badAuth int
	// upsertErrs and removeErrs are returned, in turn, before the call does
	// anything.
	upsertErrs []error
	removeErrs []error
	// upsertDelay holds an upsert's answer after it has landed.
	upsertDelay time.Duration
	// drop, when closed, ends every open stream with Unavailable.
	drop chan struct{}
}

func newFakeIngest() *fakeIngest {
	return &fakeIngest{rows: map[string]*sentinelv1.Emplacement{}, drop: make(chan struct{})}
}

func (f *fakeIngest) authed(ctx context.Context) error {
	md, _ := metadata.FromIncomingContext(ctx)
	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer "+visitorTestToken {
		f.mu.Lock()
		f.badAuth++
		f.mu.Unlock()
		return status.Error(codes.Unauthenticated, "bad token")
	}
	return nil
}

func (f *fakeIngest) put(e *sentinelv1.Emplacement) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[e.GetId()] = e
}

func (f *fakeIngest) ListEmplacements(ctx context.Context, _ *ingestv1.ListEmplacementsRequest) (*ingestv1.ListEmplacementsResponse, error) {
	if err := f.authed(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	out := &ingestv1.ListEmplacementsResponse{}
	for _, e := range f.rows {
		out.Emplacements = append(out.Emplacements, e)
	}
	return out, nil
}

func (f *fakeIngest) UpsertEmplacement(ctx context.Context, req *ingestv1.UpsertEmplacementRequest) (*ingestv1.UpsertEmplacementResponse, error) {
	if err := f.authed(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, req.GetEmplacement())
	if len(f.upsertErrs) > 0 {
		err := f.upsertErrs[0]
		f.upsertErrs = f.upsertErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	e := &sentinelv1.Emplacement{
		Id:         "emp-" + req.GetEmplacement().GetName(),
		Name:       req.GetEmplacement().GetName(),
		Mobility:   req.GetEmplacement().GetMobility(),
		SensorType: req.GetEmplacement().GetSensorType(),
		Position:   req.GetEmplacement().GetPosition(),
	}
	f.rows[e.Id] = e
	if f.upsertDelay > 0 {
		f.mu.Unlock()
		time.Sleep(f.upsertDelay)
		f.mu.Lock()
	}
	return &ingestv1.UpsertEmplacementResponse{Emplacement: e}, nil
}

func (f *fakeIngest) RemoveEmplacement(ctx context.Context, req *ingestv1.RemoveEmplacementRequest) (*ingestv1.RemoveEmplacementResponse, error) {
	if err := f.authed(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removes = append(f.removes, req.GetId())
	if len(f.removeErrs) > 0 {
		err := f.removeErrs[0]
		f.removeErrs = f.removeErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	if _, ok := f.rows[req.GetId()]; !ok {
		return nil, status.Error(codes.NotFound, "no emplacement of this connection's senders has that id")
	}
	delete(f.rows, req.GetId())
	return &ingestv1.RemoveEmplacementResponse{}, nil
}

func (f *fakeIngest) StreamReports(stream grpc.BidiStreamingServer[ingestv1.StreamReportsRequest, ingestv1.StreamReportsResponse]) error {
	if err := f.authed(stream.Context()); err != nil {
		return err
	}
	f.mu.Lock()
	f.streams++
	drop := f.drop
	f.mu.Unlock()
	reqs := make(chan *ingestv1.StreamReportsRequest)
	errc := make(chan error, 1)
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				errc <- err
				return
			}
			select {
			case reqs <- req:
			case <-stream.Context().Done():
				return
			}
		}
	}()
	for {
		select {
		case <-drop:
			return status.Error(codes.Unavailable, "dropped")
		case err := <-errc:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case req := <-reqs:
			r := req.GetReport()
			ack := &ingestv1.Ack{ClientRef: r.GetClientRef(), Status: ingestv1.AckStatus_ACK_STATUS_OK, EmplacementId: r.GetEmplacementId()}
			f.mu.Lock()
			if st := r.GetEmplacementState(); st != nil {
				f.states = append(f.states, st)
				f.refs = append(f.refs, r.GetClientRef())
				if _, ok := f.rows[st.GetEmplacementId()]; !ok {
					ack.Status, ack.Reason = ingestv1.AckStatus_ACK_STATUS_REJECTED, "unknown_emplacement"
				}
			}
			f.mu.Unlock()
			if err := stream.Send(&ingestv1.StreamReportsResponse{Ack: ack}); err != nil {
				return err
			}
		}
	}
}

// dropStreams ends every open stream; later streams stay up.
func (f *fakeIngest) dropStreams() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.drop)
	f.drop = make(chan struct{})
}

func (f *fakeIngest) stateCount(empID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.states {
		if s.GetEmplacementId() == empID {
			n++
		}
	}
	return n
}

func (f *fakeIngest) removed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removes...)
}

func (f *fakeIngest) streamCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streams
}

// serveFake serves f on addr ("127.0.0.1:0" for any port) until the test
// ends or the returned stop is called, and returns the address it listens on.
func serveFake(t *testing.T, f *fakeIngest, addr string) (string, func()) {
	t.Helper()
	var l net.Listener
	var err error
	deadline := time.Now().Add(visitorWait)
	for {
		if l, err = net.Listen("tcp", addr); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond) // the previous server's port is still closing
	}
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	ingestv1.RegisterIngestServiceServer(srv, f)
	go func() { _ = srv.Serve(l) }()
	var once sync.Once
	stop := func() { once.Do(srv.Stop) }
	t.Cleanup(stop)
	return l.Addr().String(), stop
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(visitorWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func testVisitorOptions(addr string) VisitorOptions {
	return VisitorOptions{
		Addr:        addr,
		Token:       visitorTestToken,
		Insecure:    true,
		Heartbeat:   testTick,
		RemoveAfter: time.Hour,
		RemoveRetry: 50 * time.Millisecond,
		BackoffMin:  10 * time.Millisecond,
		BackoffMax:  50 * time.Millisecond,
		DialOptions: []grpc.DialOption{grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 10 * time.Millisecond, Multiplier: 1.5, MaxDelay: 50 * time.Millisecond},
			MinConnectTimeout: time.Second,
		})},
	}
}

func startVisitors(t *testing.T, opts VisitorOptions, logger *zap.Logger) *Visitors {
	t.Helper()
	if logger == nil {
		logger = zaptest.NewLogger(t)
	}
	v := NewVisitors(opts, logger)
	if err := v.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)
	return v
}

var _ VisitorSites = (*Visitors)(nil)

func TestPlaceUpsertsBlurredFixedHumanSite(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	id, err := v.Place(context.Background(), "ab12cd34", 51.55, -0.15)
	if err != nil {
		t.Fatal(err)
	}
	if id != "emp-guest-ab12cd34" {
		t.Fatalf("id = %q", id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upserts) != 1 {
		t.Fatalf("upserts = %d", len(f.upserts))
	}
	e := f.upserts[0]
	if e.GetName() != "guest-ab12cd34" || e.GetId() != "" {
		t.Fatalf("name %q id %q", e.GetName(), e.GetId())
	}
	if e.GetMobility() != sentinelv1.Mobility_MOBILITY_FIXED || e.GetSensorType() != sentinelv1.SensorType_SENSOR_TYPE_HUMAN {
		t.Fatalf("mobility %v sensor type %v", e.GetMobility(), e.GetSensorType())
	}
	if p := e.GetPosition(); p.GetLatitude() != 51.55 || p.GetLongitude() != -0.15 || p.AltitudeMeters != nil {
		t.Fatalf("position %v", p)
	}
	if f.badAuth != 0 {
		t.Fatalf("%d calls without the token", f.badAuth)
	}
}

func TestPlaceRetriesWhileTheServiceCatchesUp(t *testing.T) {
	f := newFakeIngest()
	f.upsertErrs = []error{status.Error(codes.Unavailable, "the emplacement was saved; retry once the service has caught up")}
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil || id != "emp-guest-ab12cd34" {
		t.Fatalf("id %q err %v", id, err)
	}
}

func TestPlaceFailsOnARefusal(t *testing.T) {
	f := newFakeIngest()
	f.upsertErrs = []error{status.Error(codes.InvalidArgument, "/position/lat must be between -90 and 90")}
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	if _, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05); err == nil {
		t.Fatal("want an error")
	}
	if n := len(f.upserts); n != 1 {
		t.Fatalf("upserts = %d, want no retry", n)
	}
}

func TestOnlineSendsHeartbeatsEveryInterval(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	v.Online("ab12cd34")
	waitUntil(t, "3 heartbeats", func() bool { return f.stateCount(id) >= 3 })

	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.states {
		if s.GetHeartbeatIntervalSec() != 10 {
			t.Fatalf("state %d heartbeat_interval_sec = %d, want 10", i, s.GetHeartbeatIntervalSec())
		}
		if s.GetPosition() != nil {
			t.Fatalf("state %d carries a position", i)
		}
		if !strings.HasPrefix(f.refs[i], "guest-ab12cd34/") {
			t.Fatalf("client_ref %q", f.refs[i])
		}
	}
}

func TestOfflineStopsHeartbeatsThenRemovesAfterDelay(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	opts := testVisitorOptions(addr)
	opts.RemoveAfter = 300 * time.Millisecond
	v := startVisitors(t, opts, nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	v.Online("ab12cd34")
	waitUntil(t, "heartbeats", func() bool { return f.stateCount(id) >= 2 })
	v.Offline("ab12cd34")
	time.Sleep(3 * testTick) // a state already in flight lands
	n := f.stateCount(id)
	time.Sleep(6 * testTick)
	if got := f.stateCount(id); got != n {
		t.Fatalf("states went on after Offline: %d then %d", n, got)
	}
	if r := f.removed(); len(r) != 0 {
		t.Fatalf("removed before the delay: %v", r)
	}
	waitUntil(t, "the removal", func() bool { return len(f.removed()) > 0 })
	time.Sleep(10 * testTick)
	if r := f.removed(); len(r) != 1 || r[0] != id {
		t.Fatalf("removes = %v, want [%s]", r, id)
	}
}

func TestOnlineBeforePlaceHeartbeatsOncePlaced(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	opts := testVisitorOptions(addr)
	opts.Heartbeat = time.Hour // only an immediate heartbeat can arrive
	v := startVisitors(t, opts, nil)

	v.Online("ab12cd34")
	waitUntil(t, "the stream", func() bool { return f.streamCount() == 1 })
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	early := len(f.states)
	f.mu.Unlock()
	if early != 0 {
		t.Fatalf("%d states before Place", early)
	}
	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the first heartbeat", func() bool { return f.stateCount(id) == 1 })
}

func TestPlaceWhileOfflineSchedulesRemoval(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	opts := testVisitorOptions(addr)
	opts.RemoveAfter = 100 * time.Millisecond
	v := startVisitors(t, opts, nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the removal", func() bool { return len(f.removed()) == 1 })
	if f.stateCount(id) != 0 {
		t.Fatal("a guest never online sent heartbeats")
	}
}

func TestBackOnlineCancelsRemoval(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	opts := testVisitorOptions(addr)
	opts.RemoveAfter = 200 * time.Millisecond
	v := startVisitors(t, opts, nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	v.Online("ab12cd34")
	v.Offline("ab12cd34")
	time.Sleep(50 * time.Millisecond)
	v.Online("ab12cd34")
	n := f.stateCount(id)
	time.Sleep(400 * time.Millisecond)
	if r := f.removed(); len(r) != 0 {
		t.Fatalf("removed after coming back: %v", r)
	}
	if got := f.stateCount(id); got < n+3 {
		t.Fatalf("heartbeats did not resume: %d then %d", n, got)
	}
}

func TestReconnectsStreamAfterServerRestart(t *testing.T) {
	f := newFakeIngest()
	addr, stop := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	v.Online("ab12cd34")
	waitUntil(t, "heartbeats", func() bool { return f.stateCount(id) >= 2 })

	// A dropped stream: a new one on the same connection.
	f.dropStreams()
	n := f.stateCount(id)
	waitUntil(t, "heartbeats on a new stream", func() bool { return f.streamCount() >= 2 && f.stateCount(id) >= n+2 })

	// A restarted server: a new connection, then a new stream.
	stop()
	serveFake(t, f, addr)
	n = f.stateCount(id)
	waitUntil(t, "heartbeats after the restart", func() bool { return f.streamCount() >= 3 && f.stateCount(id) >= n+2 })
}

func TestReconcileRemovesLeftoverGuestSites(t *testing.T) {
	f := newFakeIngest()
	f.put(&sentinelv1.Emplacement{Id: "e1", Name: "guest-1"})
	f.put(&sentinelv1.Emplacement{Id: "e2", Name: "guest-2"})
	f.put(&sentinelv1.Emplacement{Id: "e3", Name: "Coastal ESM Site"})
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	v.Reconcile(context.Background())
	got := f.removed()
	if len(got) != 2 || !((got[0] == "e1" && got[1] == "e2") || (got[0] == "e2" && got[1] == "e1")) {
		t.Fatalf("removes = %v, want e1 and e2", got)
	}
}

func TestRemoveStopsHeartbeatsAndIsIdempotent(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	opts := testVisitorOptions(addr)
	opts.RemoveAfter = 50 * time.Millisecond
	v := startVisitors(t, opts, nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	v.Online("ab12cd34")
	waitUntil(t, "heartbeats", func() bool { return f.stateCount(id) >= 1 })
	v.Remove(context.Background(), "ab12cd34")
	v.Remove(context.Background(), "ab12cd34")
	if r := f.removed(); len(r) != 1 || r[0] != id {
		t.Fatalf("removes = %v", r)
	}
	// Presence after the remove resurrects nothing, and the offline delay
	// finds nothing to remove.
	time.Sleep(2 * testTick)
	n := f.stateCount(id)
	v.Offline("ab12cd34")
	v.Online("ab12cd34")
	v.Offline("ab12cd34")
	time.Sleep(200 * time.Millisecond)
	if got := f.stateCount(id); got != n {
		t.Fatalf("states after Remove: %d then %d", n, got)
	}
	if r := f.removed(); len(r) != 1 {
		t.Fatalf("removes = %v", r)
	}
}

func TestRemoveTreatsNotFoundAsDone(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.rows, id) // an operator deleted it
	f.mu.Unlock()
	v.Remove(context.Background(), "ab12cd34")
	time.Sleep(200 * time.Millisecond) // no retry follows
	if r := f.removed(); len(r) != 1 {
		t.Fatalf("removes = %v", r)
	}
}

func TestRemoveRetriesAFailure(t *testing.T) {
	f := newFakeIngest()
	f.removeErrs = []error{status.Error(codes.Internal, "boom")}
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	v.Online("ab12cd34")
	v.Remove(context.Background(), "ab12cd34")
	waitUntil(t, "the retry", func() bool { return len(f.removed()) == 2 })
	f.mu.Lock()
	_, still := f.rows[id]
	f.mu.Unlock()
	if still {
		t.Fatal("the site survived the retry")
	}
	n := f.stateCount(id)
	time.Sleep(5 * testTick)
	if got := f.stateCount(id); got != n {
		t.Fatal("a removed site heartbeat")
	}
	if r := f.removed(); len(r) != 2 {
		t.Fatalf("removes = %v", r)
	}
}

func TestRemoveAfterAFailedPlaceFindsTheSiteByName(t *testing.T) {
	f := newFakeIngest()
	f.upsertDelay = 300 * time.Millisecond
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	// The upsert lands but its answer is lost: the caller's deadline ends.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := v.Place(ctx, "ab12cd34", 1.05, 2.05); err == nil {
		t.Fatal("want an error")
	}
	v.Remove(context.Background(), "ab12cd34")
	if r := f.removed(); len(r) != 1 || r[0] != "emp-guest-ab12cd34" {
		t.Fatalf("removes = %v", r)
	}
}

func TestRemoveOfANeverPlacedGuestCallsNothing(t *testing.T) {
	f := newFakeIngest()
	addr, _ := serveFake(t, f, "127.0.0.1:0")
	v := startVisitors(t, testVisitorOptions(addr), nil)

	v.Online("ab12cd34")
	v.Remove(context.Background(), "ab12cd34")
	v.Remove(context.Background(), "nobody00")
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.removes) != 0 || f.lists != 0 {
		t.Fatalf("removes %v lists %d", f.removes, f.lists)
	}
}

// TestOnlineOfflineDoNotBlock: presence calls return at once even while the
// service hangs.
func TestOnlineOfflineDoNotBlock(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0") // accepts, never answers
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	v := startVisitors(t, testVisitorOptions(l.Addr().String()), nil)
	done := make(chan struct{})
	go func() {
		for range 1000 {
			v.Online("ab12cd34")
			v.Offline("ab12cd34")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(visitorWait):
		t.Fatal("presence calls blocked")
	}
}

func TestStartNeedsAnAddressAndToken(t *testing.T) {
	for _, o := range []VisitorOptions{{Token: "t", Insecure: true}, {Addr: "127.0.0.1:1", Insecure: true}} {
		v := NewVisitors(o, zap.NewNop())
		if err := v.Start(context.Background()); err == nil {
			v.Stop()
			t.Fatalf("Start(%+v) succeeded", o)
		}
	}
}

func TestPlaceBeforeStartFails(t *testing.T) {
	v := NewVisitors(VisitorOptions{Addr: "127.0.0.1:1", Token: "t", Insecure: true}, zap.NewNop())
	if _, err := v.Place(context.Background(), "ab12cd34", 1, 2); err == nil {
		t.Fatal("want an error")
	}
	v.Remove(context.Background(), "ab12cd34") // no panic
	v.Stop()
}

func TestStopEndsEveryGoroutine(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	f := newFakeIngest()
	addr, stopServer := serveFake(t, f, "127.0.0.1:0")
	opts := testVisitorOptions(addr)
	opts.RemoveAfter = 10 * time.Millisecond
	v := NewVisitors(opts, zap.NewNop())
	if err := v.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, err := v.Place(context.Background(), "ab12cd34", 1.05, 2.05)
	if err != nil {
		t.Fatal(err)
	}
	v.Online("ab12cd34")
	waitUntil(t, "heartbeats", func() bool { return f.stateCount(id) >= 2 })
	v.Offline("ab12cd34")
	f.dropStreams()
	time.Sleep(50 * time.Millisecond)
	v.Stop()
	v.Stop() // idempotent
	stopServer()
}

func TestNeverLogsTheTokenOrCoordinates(t *testing.T) {
	f := newFakeIngest()
	addr, stop := serveFake(t, f, "127.0.0.1:0")
	var buf syncBuffer
	logger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewDevelopmentEncoderConfig()), zapcore.AddSync(&buf), zapcore.DebugLevel))
	v := startVisitors(t, testVisitorOptions(addr), logger)
	if _, err := v.Place(context.Background(), "ab12cd34", 89.8765, -179.4321); err != nil {
		t.Fatal(err)
	}
	v.Online("ab12cd34")
	stop() // stream errors are logged
	time.Sleep(100 * time.Millisecond)
	v.Stop()
	out := buf.String()
	for _, bad := range []string{visitorTestToken, "89.8765", "179.4321"} {
		if strings.Contains(out, bad) {
			t.Fatalf("log contains %q:\n%s", bad, out)
		}
	}
}

// syncBuffer is a log sink safe for the client's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
