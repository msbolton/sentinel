package grpcin

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
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

// fakeEmplacements is an EmplacementSource over a fixed list.
type fakeEmplacements struct{ rows []emplacement.Emplacement }

func (f *fakeEmplacements) All() []emplacement.Emplacement { return f.rows }
func (f *fakeEmplacements) Get(id string) (emplacement.Emplacement, bool) {
	for _, e := range f.rows {
		if e.ID == id {
			return e, true
		}
	}
	return emplacement.Emplacement{}, false
}
func (f *fakeEmplacements) ByFeed(feedID string) []emplacement.Emplacement {
	var out []emplacement.Emplacement
	for _, e := range f.rows {
		if e.FeedID != nil && *e.FeedID == feedID {
			out = append(out, e)
		}
	}
	return out
}
func (f *fakeEmplacements) OnChange(func(string)) (cancel func()) { return func() {} }

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
	dv, err := New(spec(cfg, secrets), driver.Deps{Logger: zap.NewNop(), ListenerHost: "sentinel.example", Tokens: tokens, Emplacements: emps})
	if err != nil {
		t.Fatal(err)
	}
	d := dv.(*Driver)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, sink) }()
	r := &rig{d: d, sink: sink, tokens: tokens, port: cfg["port"].(int), cancel: cancel, done: done}
	t.Cleanup(func() { r.stop(t) })
	waitFor(t, func() bool { return d.connected.Load() })
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
