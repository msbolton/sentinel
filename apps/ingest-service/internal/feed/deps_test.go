package feed

import (
	"context"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type depsTokens struct{ ready chan struct{} }

func (*depsTokens) Authenticate(string) (ingesttoken.Token, bool)    { return ingesttoken.Token{}, false }
func (*depsTokens) OnRevoke(func(ingesttoken.Token)) (cancel func()) { return func() {} }
func (t *depsTokens) Ready() <-chan struct{}                         { return t.ready }

type depsEmplacements struct{}

func (depsEmplacements) All() []emplacement.Emplacement { return nil }
func (depsEmplacements) Get(string) (emplacement.Emplacement, bool) {
	return emplacement.Emplacement{}, false
}
func (depsEmplacements) ByFeed(string) []emplacement.Emplacement  { return nil }
func (depsEmplacements) OnChange(func(id string)) (cancel func()) { return func() {} }

// The drivers that authenticate senders (grpc) get the token cache, and the
// emplacement view, from the supervisor's options.
func TestDriversGetTheTokenCacheAndEmplacements(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(feedSpec("a", 1, true))
	l := newFakeLeaser("r1")
	tokens := &depsTokens{ready: make(chan struct{})}
	var (
		mu  sync.Mutex
		got driver.Deps
	)
	sup := New(reg, &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
		ReplicaID: "r1", Leaser: &replicaLeaser{fakeLeaser: l, me: "r1"}, Preferred: preferAlways("r1"),
		Tokens: tokens, Emplacements: depsEmplacements{},
		Factory: func(_ driver.Spec, deps driver.Deps) (driver.Driver, error) {
			mu.Lock()
			got = deps
			mu.Unlock()
			return newMockDriver(), nil
		},
	})
	sup.Reconcile(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if got.Tokens != tokens {
		t.Errorf("deps.Tokens = %v, want the supervisor's token cache", got.Tokens)
	}
	if got.Emplacements == nil {
		t.Error("deps.Emplacements is nil")
	}
}
