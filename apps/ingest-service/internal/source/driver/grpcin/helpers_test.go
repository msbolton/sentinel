package grpcin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
)

const waitTimeout = 5 * time.Second

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// fakeTokens is a TokenSource whose tokens the test controls.
type fakeTokens struct {
	mu       sync.Mutex
	bySecret map[string]ingesttoken.Token
	subs     map[int]func(ingesttoken.Token)
	next     int
	ready    chan struct{}
	// failAfter, when > 0, makes Authenticate fail from that call on (the
	// token revoked between two checks).
	calls, failAfter int
}

func newFakeTokens() *fakeTokens {
	r := make(chan struct{})
	close(r)
	return &fakeTokens{bySecret: map[string]ingesttoken.Token{}, subs: map[int]func(ingesttoken.Token){}, ready: r}
}

func (f *fakeTokens) add(secret string, t ingesttoken.Token) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bySecret[secret] = t
}

func (f *fakeTokens) Authenticate(secret string) (ingesttoken.Token, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failAfter > 0 && f.calls >= f.failAfter {
		return ingesttoken.Token{}, false
	}
	t, ok := f.bySecret[secret]
	if !ok || !t.Usable(time.Now()) {
		return ingesttoken.Token{}, false
	}
	return t, true
}

func (f *fakeTokens) OnRevoke(fn func(ingesttoken.Token)) (cancel func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.next
	f.next++
	f.subs[id] = fn
	return func() { f.mu.Lock(); delete(f.subs, id); f.mu.Unlock() }
}

func (f *fakeTokens) Ready() <-chan struct{} { return f.ready }

// revoke marks the token revoked and tells the subscribers, as the cache does.
func (f *fakeTokens) revoke(secret string) {
	f.mu.Lock()
	t := f.bySecret[secret]
	now := time.Now()
	t.RevokedAt = &now
	f.bySecret[secret] = t
	subs := make([]func(ingesttoken.Token), 0, len(f.subs))
	for _, fn := range f.subs {
		subs = append(subs, fn)
	}
	f.mu.Unlock()
	for _, fn := range subs {
		fn(t)
	}
}

// fakeEmplacements is an EmplacementSource over a list the test controls.
type fakeEmplacements struct {
	mu   sync.Mutex
	rows []emplacement.Emplacement
	// notReady, when set, leaves Ready open (the cache has not loaded).
	notReady bool
}

func (f *fakeEmplacements) Ready() <-chan struct{} {
	r := make(chan struct{})
	if !f.notReady {
		close(r)
	}
	return r
}

// put replaces the row with e's ID, or appends e.
func (f *fakeEmplacements) put(e emplacement.Emplacement) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].ID == e.ID {
			f.rows[i] = e
			return
		}
	}
	f.rows = append(f.rows, e)
}

func (f *fakeEmplacements) All() []emplacement.Emplacement {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]emplacement.Emplacement(nil), f.rows...)
}
func (f *fakeEmplacements) Get(id string) (emplacement.Emplacement, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.rows {
		if e.ID == id {
			return e, true
		}
	}
	return emplacement.Emplacement{}, false
}
func (f *fakeEmplacements) ByFeed(feedID string) []emplacement.Emplacement {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []emplacement.Emplacement
	for _, e := range f.rows {
		if e.FeedID != nil && *e.FeedID == feedID {
			out = append(out, e)
		}
	}
	return out
}
func (f *fakeEmplacements) OnChange(func(string)) (cancel func()) { return func() {} }

// touchCall is one Touch the writer recorded.
type touchCall struct {
	id  string
	pos *emplacement.Point
	at  time.Time
}

// fakeWriter is a driver.EmplacementWriter over a fakeEmplacements, so a
// write is visible to the driver's cache view at once.
type fakeWriter struct {
	emps *fakeEmplacements

	// beforeUpdate, when set, runs at the start of every Update, before the
	// row is read: a test changes the row between the driver's Get and Update.
	beforeUpdate func(id string)

	// lag, when set, leaves the driver's cache view one version behind every
	// write (the cache has not caught up), though the write itself succeeds.
	lag bool

	mu      sync.Mutex
	n       int
	touched []touchCall
}

func (w *fakeWriter) Create(_ context.Context, e emplacement.Emplacement) (emplacement.Emplacement, error) {
	for _, r := range w.emps.All() {
		if strings.EqualFold(r.Name, e.Name) {
			return emplacement.Emplacement{}, emplacement.ErrNameTaken
		}
	}
	w.mu.Lock()
	w.n++
	e.ID = fmt.Sprintf("e-%d", w.n)
	w.mu.Unlock()
	e.Version = 1
	w.put(e)
	return e, nil
}

func (w *fakeWriter) Update(_ context.Context, id string, ifVersion int, fn func(*emplacement.Emplacement) error) (emplacement.Emplacement, error) {
	if w.beforeUpdate != nil {
		w.beforeUpdate(id)
	}
	e, ok := w.emps.Get(id)
	if !ok {
		return emplacement.Emplacement{}, emplacement.ErrNotFound
	}
	if ifVersion > 0 && e.Version != ifVersion {
		return emplacement.Emplacement{}, emplacement.ErrVersionConflict
	}
	if err := fn(&e); err != nil {
		return emplacement.Emplacement{}, err
	}
	e.Version++
	w.put(e)
	return e, nil
}

// put stores a written row in the cache view the driver reads.
func (w *fakeWriter) put(e emplacement.Emplacement) {
	if w.lag {
		stale := e
		stale.Version--
		w.emps.put(stale)
		return
	}
	w.emps.put(e)
}

func (w *fakeWriter) Get(_ context.Context, id string) (emplacement.Emplacement, error) {
	if e, ok := w.emps.Get(id); ok {
		return e, nil
	}
	return emplacement.Emplacement{}, emplacement.ErrNotFound
}

func (w *fakeWriter) List(_ context.Context, f emplacement.Filter) ([]emplacement.Emplacement, error) {
	if f.FeedID != "" {
		return w.emps.ByFeed(f.FeedID), nil
	}
	return w.emps.All(), nil
}

func (w *fakeWriter) Delete(_ context.Context, id string) error {
	w.emps.mu.Lock()
	defer w.emps.mu.Unlock()
	for i := range w.emps.rows {
		if w.emps.rows[i].ID == id {
			w.emps.rows = append(w.emps.rows[:i], w.emps.rows[i+1:]...)
			return nil
		}
	}
	return emplacement.ErrNotFound
}

func (w *fakeWriter) Touch(_ context.Context, id string, pos *emplacement.Point, _ *float64, at time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.touched = append(w.touched, touchCall{id: id, pos: pos, at: at})
	return nil
}

func (w *fakeWriter) touches() []touchCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]touchCall(nil), w.touched...)
}

// emp is a live emplacement fixture; a fixed one gets a position.
func emp(id, feed, mobility, sensor, class string) emplacement.Emplacement {
	e := emplacement.Emplacement{ID: id, Name: id, Mobility: mobility, SensorType: sensor, Classification: class,
		Active: true, AzimuthReference: emplacement.AzimuthTrueNorth, Version: 1}
	if feed != "" {
		f := feed
		e.FeedID = &f
	}
	if mobility == emplacement.MobilityFixed {
		e.Position, e.Altitude = &emplacement.Point{Lat: 50.1, Lon: -4.2}, ptr(0.0)
	}
	return e
}

// claiming makes a report claim an emplacement.
func claiming(r *ingestv1.Report, id string) *ingestv1.Report {
	r.EmplacementId = id
	return r
}

type deadLetter struct {
	reason string
	p      driver.Provenance
}

// memSink records what the driver emits. block, when set, holds every Emit
// until it is closed or the context ends.
type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	letters []deadLetter
	block   chan struct{}
}

func (s *memSink) Emit(ctx context.Context, o *obs.Observation) error {
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, o)
	return nil
}
func (s *memSink) DeadLetter([]byte, string, error) {}
func (s *memSink) Skipped(string)                   {}
func (s *memSink) Succeeded(int)                    {}
func (s *memSink) Blocked(time.Duration)            {}
func (s *memSink) DeadLetterFrom(_ []byte, reason string, _ error, p driver.Provenance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.letters = append(s.letters, deadLetter{reason, p})
}
func (s *memSink) emitted() []*obs.Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*obs.Observation(nil), s.obs...)
}
func (s *memSink) deadLetters() []deadLetter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]deadLetter(nil), s.letters...)
}

type rig struct {
	d      *Driver
	sink   *memSink
	tokens *fakeTokens
	port   int
	cancel context.CancelFunc
	done   chan error
}

func spec(cfg map[string]any, secrets map[string]string) driver.Spec {
	raw, _ := json.Marshal(cfg)
	return driver.Spec{FeedID: "grpc-1", Driver: ID, Config: raw, Secrets: secrets}
}

// start runs a driver on a free loopback port until the test ends. cfg may
// leave out port and address.
func start(t *testing.T, cfg map[string]any, secrets map[string]string, tokens *fakeTokens, emps *fakeEmplacements, sink *memSink) *rig {
	t.Helper()
	return startWith(t, cfg, secrets, tokens, emps, nil, sink, nil)
}

// startWith is start, after tweak changed the driver (unexported knobs).
func startWith(t *testing.T, cfg map[string]any, secrets map[string]string, tokens *fakeTokens, emps *fakeEmplacements, writer *fakeWriter, sink *memSink, tweak func(*Driver)) *rig {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, ok := cfg["port"]; !ok {
		cfg["port"] = freePort(t)
	}
	cfg["address"] = "127.0.0.1"
	if tokens == nil {
		tokens = newFakeTokens()
	}
	if emps == nil {
		emps = &fakeEmplacements{}
	}
	if sink == nil {
		sink = &memSink{}
	}
	deps := driver.Deps{Logger: zap.NewNop(), ListenerHost: "sentinel.example", Tokens: tokens, Emplacements: emps}
	if writer != nil { // a nil *fakeWriter must not become a non-nil interface
		deps.EmplacementWriter = writer
	}
	dv, err := New(spec(cfg, secrets), deps)
	if err != nil {
		t.Fatal(err)
	}
	d := dv.(*Driver)
	if tweak != nil {
		tweak(d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, sink) }()
	r := &rig{d: d, sink: sink, tokens: tokens, port: cfg["port"].(int), cancel: cancel, done: done}
	t.Cleanup(func() { r.stop(t) })
	deadline := time.After(waitTimeout)
	for !d.connected.Load() {
		select {
		case err := <-done:
			done <- err // stop and later calls see it too
			t.Fatalf("Run returned before serving: %v", err)
		case <-deadline:
			t.Fatal("the driver did not start serving in time")
		case <-time.After(5 * time.Millisecond):
		}
	}
	return r
}

// stop cancels the run and waits for Run to return, failing past the bound.
func (r *rig) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		r.done <- err // later calls see it too
		return err
	case <-time.After(stopGrace + 2*time.Second):
		t.Fatal("Run did not return in time after cancel")
		return nil
	}
}

func (r *rig) addr() string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(r.port)) }

// client dials the rig without TLS.
func (r *rig) client(t *testing.T) ingestv1.IngestServiceClient {
	t.Helper()
	cc, err := grpc.NewClient("passthrough:///"+r.addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })
	return ingestv1.NewIngestServiceClient(cc)
}
