package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/dlq"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/mgmt"
	"github.com/sentinel/ingest-service/internal/registry"
)

// A failed schema check only means the schema is out of date when CheckSchema
// found columns missing. A timeout or a permissions error says nothing about
// the schema, and telling the operator to apply db/schema for one would hide
// the real cause.
func TestOnceRunsTheCloseOnceWithTheFirstBound(t *testing.T) {
	var got []time.Duration
	f := once(func(d time.Duration) { got = append(got, d) })
	f(time.Second)
	f(time.Hour)
	if len(got) != 1 || got[0] != time.Second {
		t.Errorf("close ran with %v, want once with the first bound", got)
	}
}

func TestSchemaCheckMessage(t *testing.T) {
	missing := &registry.SchemaError{Missing: []registry.SchemaColumn{
		{Table: "ingest_replicas", Column: "listener", File: "db/schema/70_feeds.sql"},
	}}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"columns missing", missing, "database schema is out of date"},
		{"columns missing, wrapped", fmt.Errorf("startup: %w", missing), "database schema is out of date"},
		{
			"a query failure, wrapped as CheckSchema wraps it",
			fmt.Errorf("checking the database schema: %w", errors.New("permission denied for view columns")),
			"checking the database schema failed",
		},
		{
			"a timeout",
			fmt.Errorf("checking the database schema: %w", context.DeadlineExceeded),
			"checking the database schema failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := schemaCheckMessage(tc.err); got != tc.want {
				t.Errorf("schemaCheckMessage(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// oneFeed is a store that has one feed and nothing else; the routes under test
// read no more than that.
type oneFeed struct{ mgmt.FeedStore }

func (oneFeed) Get(_ context.Context, id string) (registry.Feed, error) {
	return registry.Feed{ID: id, Name: id, Config: json.RawMessage("{}")}, nil
}

// DLQ_PEEK_MAX_SCAN=0 turns dead-letter viewing off: the management API gets
// no reader, so the endpoint answers 501 (which the client shows as "not
// enabled"), and nothing is created that needs closing. Any other value gives
// it the reader, which contacts Kafka only when it is first asked.
func TestServeDeadLetters(t *testing.T) {
	get := func(h *mgmt.Handler) *http.Response {
		t.Helper()
		mux := http.NewServeMux()
		h.RegisterRoutes(mux, mgmt.Auth(mgmt.AuthOptions{}))
		srv := httptest.NewServer(mux)
		defer srv.Close()
		req, _ := http.NewRequest("GET", srv.URL+"/feeds/pier/deadletters", nil)
		req.Header.Set(middleware.HeaderUserRoles, "sentinel-operator")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	t.Run("off", func(t *testing.T) {
		cfg := config.Load()
		cfg.DLQPeekMaxScan = 0
		h := mgmt.NewHandler(oneFeed{}, nil, nil, nil)
		stop := serveDeadLetters(h, cfg, zap.NewNop())
		defer stop(time.Second)
		// A nil reader, not a nil *KafkaReader in a Reader: that one is not nil
		// to the handler, which would call it.
		if h.DeadLetters != nil {
			t.Fatalf("the handler has a reader (%T) with DLQ_PEEK_MAX_SCAN=0", h.DeadLetters)
		}
		if resp := get(h); resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("GET deadletters with the endpoint off: %d, want 501", resp.StatusCode)
		}
	})

	t.Run("on", func(t *testing.T) {
		cfg := config.Load()
		cfg.DLQPeekMaxScan = 2000
		h := mgmt.NewHandler(oneFeed{}, nil, nil, nil)
		stop := serveDeadLetters(h, cfg, zap.NewNop())
		if _, ok := h.DeadLetters.(*dlq.KafkaReader); !ok {
			t.Fatalf("the handler's reader is %T, want the Kafka reader", h.DeadLetters)
		}
		// Nothing was read, so no consumer exists to close; closing is still safe,
		// and a read after it is refused instead of reaching for Kafka.
		stop(time.Second)
		stop(time.Second) // as the deferred stop after the shutdown's does
		if _, _, _, err := h.DeadLetters.Recent(context.Background(), "pier", 5); !errors.Is(err, dlq.ErrClosed) {
			t.Errorf("a read after stop: %v, want dlq.ErrClosed", err)
		}
	})
}

type feedList []registry.Feed

func (l feedList) List(context.Context) ([]registry.Feed, error) { return l, nil }

// Review focus 2: a status row older than the stale window belongs to a
// replica that went silent, and its heartbeats are not believed.
func TestLivenessSourceBelievesOnlyFreshStatuses(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	fresh, stale := now.Add(-5*time.Second), now.Add(-time.Minute)
	detail := json.RawMessage(`{"emplacements":[{"id":"e-1","name":"A","classification":"UNCLASSIFIED","state":"online","intervalSec":5}]}`)
	src := livenessSource(feedList{
		{ID: "live", Driver: "grpc", Status: &registry.Status{Owner: "r1", UpdatedAt: &fresh, Detail: detail}},
		{ID: "dead", Driver: "grpc", Status: &registry.Status{Owner: "r2", UpdatedAt: &stale, Detail: detail}},
		{ID: "never", Driver: "grpc"},
		{ID: "tcp", Driver: "tcp"},
	}, 15*time.Second, func() time.Time { return now })
	got, err := src(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l := got["live"].Emplacements; len(l) != 1 || l[0].State != emplacement.HeartbeatOnline {
		t.Errorf("live = %+v", got["live"])
	}
	if got["dead"].Emplacements != nil {
		t.Error("a silent replica's status was believed")
	}
	if got["never"].Emplacements != nil || got["tcp"].Driver != "tcp" {
		t.Errorf("never = %+v, tcp = %+v", got["never"], got["tcp"])
	}
}
