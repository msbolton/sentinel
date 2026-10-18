package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests need a database with the baseline applied. Set
// TEST_DATABASE_URL to run them; they create and delete feeds named test-*.
func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	return testStoreWith(t, nil)
}

// testStoreWith is testStore on a pool that tweak has changed before it
// connects. The feeds named test-* are deleted before the test as well as
// after it: what a crashed run left behind still holds its ports and ids, and
// every guarded create on them would be refused.
func testStoreWith(t *testing.T, tweak func(*pgxpool.Config)) (*Store, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if tweak != nil {
		tweak(cfg)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := NewWithPool(pool, nil)
	dropTestFeeds := func(ctx context.Context) error {
		_, err := pool.Exec(ctx, `DELETE FROM sentinel.feeds WHERE id LIKE 'test-%'`)
		return err
	}
	if err := dropTestFeeds(ctx); err != nil {
		t.Fatalf("removing the test feeds an earlier run left: %v", err)
	}
	t.Cleanup(func() { _ = dropTestFeeds(context.Background()) })
	return s, ctx
}

func TestStoreCRUDAndNotify(t *testing.T) {
	s, ctx := testStore(t)
	changes := s.Watch(ctx)
	time.Sleep(200 * time.Millisecond) // let LISTEN attach

	f := &Feed{ID: "test-mqtt", Name: "Test", Driver: "stream",
		Config:     json.RawMessage(`{"transport":{"kind":"mqtt","brokerUrl":"tcp://b:1883","topics":["a"]},"decoder":{"name":"json"}}`),
		SecretRefs: map[string]string{}, Enabled: true, CreatedBy: "u1"}
	if err := s.Create(ctx, f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || f.Status == nil || f.Status.State != StateStopped {
		t.Fatalf("created feed = %+v status %+v", f, f.Status)
	}
	if err := s.Create(ctx, &Feed{ID: "test-mqtt", Name: "dup", Driver: "stream"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create err = %v", err)
	}
	expectChange(t, changes, ChannelFeeds, "test-mqtt")

	f.Name = "Renamed"
	if err := s.Update(ctx, f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 2 || f.Name != "Renamed" {
		t.Fatalf("updated feed = %+v", f)
	}
	expectChange(t, changes, ChannelFeeds, "test-mqtt")

	got, err := s.SetEnabled(ctx, "test-mqtt", false)
	if err != nil || got.Enabled || got.Version != 3 {
		t.Fatalf("SetEnabled = %+v err %v", got, err)
	}
	expectChange(t, changes, ChannelFeeds, "test-mqtt")

	exp := time.Now().Add(90 * time.Second).UTC().Truncate(time.Millisecond)
	if err := s.SetArea(ctx, Area{FeedID: "test-mqtt", Active: true, Clients: 2, Boxes: []Box{{North: 1, South: 0, East: 1, West: 0}}, ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	expectChange(t, changes, ChannelAreas, "test-mqtt")
	a, err := s.GetArea(ctx, "test-mqtt")
	if err != nil || !a.Active || a.Clients != 2 || len(a.Boxes) != 1 || a.ExpiresAt == nil {
		t.Fatalf("GetArea = %+v err %v", a, err)
	}
	if err := s.SetArea(ctx, Area{FeedID: "test-missing", Active: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("area for unknown feed err = %v", err)
	}

	now := time.Now().UTC()
	if err := s.UpsertStatus(ctx, Status{FeedID: "test-mqtt", Owner: "r1", State: StateRunning, Health: HealthHealthy, LastSuccessAt: &now, MessagesTotal: 5, AppliedVersion: 3}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "test-mqtt")
	if got.Status.Owner != "r1" || got.Status.State != StateRunning || got.Status.MessagesTotal != 5 {
		t.Fatalf("status = %+v", got.Status)
	}
	if err := s.ReleaseStatuses(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "test-mqtt")
	if got.Status.Owner != "" || got.Status.State != StateStopped {
		t.Fatalf("released status = %+v", got.Status)
	}

	if err := s.Delete(ctx, "test-mqtt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "test-mqtt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
	if err := s.Delete(ctx, "test-mqtt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete err = %v", err)
	}
}

func TestEnsureBuiltins(t *testing.T) {
	s, ctx := testStore(t)
	if err := s.EnsureBuiltins(ctx, []string{"celestrak"}); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]Feed{}
	for _, f := range feeds {
		seen[f.ID] = f
	}
	for _, b := range Builtins {
		if f, ok := seen[b.ID]; !ok || !f.Builtin || f.Driver != b.Driver {
			t.Errorf("builtin %s missing or wrong: %+v", b.ID, f)
		}
	}
	// Idempotent: a second call changes nothing.
	if err := s.EnsureBuiltins(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

// LiveReplicas reports every unexpired replica with its role: an ordinary
// replica, a dedicated listener and a listener that also takes ordinary feeds.
func TestLiveReplicasReportsRoles(t *testing.T) {
	s, ctx := testStore(t)
	const prefix = "test-roles-"
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM sentinel.ingest_replicas WHERE replica_id LIKE $1`, prefix+"%")
	})
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO sentinel.ingest_replicas (replica_id, expires_at, listener, listener_shared, listener_host) VALUES
		  ($1, NOW() + INTERVAL '1 minute', TRUE,  FALSE, 'ingest.example'),
		  ($2, NOW() + INTERVAL '1 minute', TRUE,  TRUE,  NULL),
		  ($3, NOW() + INTERVAL '1 minute', FALSE, FALSE, NULL),
		  ($4, NOW() - INTERVAL '1 minute', TRUE,  FALSE, 'gone.example')`,
		prefix+"dedicated", prefix+"shared", prefix+"plain", prefix+"expired"); err != nil {
		t.Fatal(err)
	}

	live, err := s.LiveReplicas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []Replica // the database may hold replicas of a running stack too
	for _, r := range live {
		if strings.HasPrefix(r.ID, prefix) {
			got = append(got, r)
		}
	}
	want := []Replica{
		{ID: prefix + "dedicated", Listener: true, Host: "ingest.example"},
		{ID: prefix + "plain"},
		{ID: prefix + "shared", Listener: true, Shared: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("live replicas = %+v, want %+v (sorted by id, the expired one left out)", got, want)
	}
}

// udpFeed is a udp connection on port, as the management API stores one.
func udpFeed(id string, port int) Feed {
	return Feed{ID: id, Name: id, Driver: "udp", CreatedBy: "u1", SecretRefs: map[string]string{},
		Config: json.RawMessage(fmt.Sprintf(`{"port":%d}`, port))}
}

// portGuard is the port check the management API hands the store.
func portGuard(t *testing.T) Guard {
	t.Helper()
	reserved, err := ParsePortSet(DefaultReservedPorts)
	if err != nil {
		t.Fatal(err)
	}
	return func(f Feed, others []Feed) []Problem { return CheckPorts(f, others, reserved) }
}

// Saves that choose the same port at the same moment: the port check and the
// insert are one step under an advisory lock, so exactly one connection is
// stored and the other is refused for its port.
func TestConcurrentCreatesCannotShareAPort(t *testing.T) {
	s, ctx := testStore(t)
	check := portGuard(t)
	// The guard dawdles once it has read the feeds: without the lock both
	// saves would read before either wrote, and both would be stored.
	guard := func(f Feed, others []Feed) []Problem {
		time.Sleep(20 * time.Millisecond)
		return check(f, others)
	}
	for round := 0; round < 20; round++ {
		port := 47000 + round // a port per round: the rows of earlier rounds stay until the test ends
		ids := []string{fmt.Sprintf("test-race-%d-a", round), fmt.Sprintf("test-race-%d-b", round)}
		errs := make([]error, len(ids))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f := udpFeed(id, port)
				<-start
				errs[i] = s.CreateGuarded(ctx, &f, guard)
			}()
		}
		close(start)
		wg.Wait()

		stored := 0
		for i, err := range errs {
			if err == nil {
				stored++
				continue
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Path != "/config/port" {
				t.Fatalf("round %d: creating %s failed with %v, want a refusal for /config/port", round, ids[i], err)
			}
		}
		if stored != 1 {
			t.Fatalf("round %d: %d of %d creates on port %d were stored (%v), want exactly 1", round, stored, len(ids), port, errs)
		}
	}
}

// A guarded save reads the feeds only once it holds the lock, so it sees what
// the save that held the lock before it committed, and refuses a port that
// save took. A refused save changes nothing.
func TestUpdateGuardedSeesCommittedFeeds(t *testing.T) {
	s, ctx := testStore(t)
	check := portGuard(t)

	mover := udpFeed("test-sees-mover", 47100)
	if err := s.Create(ctx, &mover); err != nil {
		t.Fatal(err)
	}

	// Another save is midway: it holds the lock and has inserted a connection
	// on port 47101 that is not committed yet.
	other, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Rollback(context.Background()) }) // a no-op once committed
	if _, err := other.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, feedPortsLockKey); err != nil {
		t.Fatal(err)
	}
	taken := udpFeed("test-sees-taken", 47101)
	if _, err := other.Exec(ctx, `
		INSERT INTO sentinel.feeds (id, name, driver, config) VALUES ($1, $2, $3, $4)`,
		taken.ID, taken.Name, taken.Driver, taken.Config); err != nil {
		t.Fatal(err)
	}

	// The mover asks for that port. It waits for the lock instead of reading
	// the feeds without the other save's connection.
	moved := mover
	moved.Config = json.RawMessage(`{"port":47101}`)
	result := make(chan error, 1)
	go func() { result <- s.UpdateGuarded(ctx, &moved, check) }()
	select {
	case err := <-result:
		t.Fatalf("UpdateGuarded returned (%v) while another save held the lock", err)
	case <-time.After(300 * time.Millisecond):
	}

	// Once the other save commits, the mover sees its connection and is refused.
	if err := other.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("UpdateGuarded still waiting after the other save committed")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Path != "/config/port" || !strings.Contains(ve.Problems[0].Message, taken.ID) {
		t.Fatalf("UpdateGuarded = %v, want a refusal for /config/port naming %s", err, taken.ID)
	}
	got, err := s.Get(ctx, mover.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || !strings.Contains(string(got.Config), "47100") {
		t.Fatalf("refused save changed the feed: version %d config %s", got.Version, got.Config)
	}

	// A free port goes through, and so does the port the feed now holds: it
	// does not clash with itself.
	moved.Config = json.RawMessage(`{"port":47102}`)
	for save := 1; save <= 2; save++ {
		if err := s.UpdateGuarded(ctx, &moved, check); err != nil {
			t.Fatalf("save %d to port 47102: %v", save, err)
		}
		if moved.Version != 1+save {
			t.Fatalf("version after save %d = %d, want %d", save, moved.Version, 1+save)
		}
	}
}

// Guarded saves rely on the read after the lock wait seeing the previous
// holder's commit, which only READ COMMITTED gives. A server whose default is a
// stricter level must not change that.
func TestTransactionsRunReadCommittedWhateverTheServerDefault(t *testing.T) {
	s, ctx := testStoreWith(t, func(cfg *pgxpool.Config) {
		cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	})
	var serverDefault, got string
	if err := s.pool.QueryRow(ctx, `SELECT current_setting('default_transaction_isolation')`).Scan(&serverDefault); err != nil {
		t.Fatal(err)
	}
	if serverDefault != "repeatable read" {
		t.Fatalf("test setup: the connections default to %q, want repeatable read", serverDefault)
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_setting('transaction_isolation')`).Scan(&got)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "read committed" {
		t.Fatalf("a store transaction runs %q on a server that defaults to %q, want read committed", got, serverDefault)
	}
}

// A guard that panics must not leave its transaction open holding the port
// lock: the next guarded save goes through, and the connection is back in the
// pool.
func TestPanickingGuardReleasesThePortLock(t *testing.T) {
	s, ctx := testStore(t)

	crashed := udpFeed("test-panic-crashed", 47200)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the guard's panic did not reach the caller")
			}
		}()
		_ = s.CreateGuarded(ctx, &crashed, func(Feed, []Feed) []Problem { panic("guard failed") })
	}()

	// With the lock still held, this would wait on it until its context ran out.
	next, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	after := udpFeed("test-panic-after", 47201)
	if err := s.CreateGuarded(next, &after, portGuard(t)); err != nil {
		t.Fatalf("a guarded save after the panic: %v", err)
	}
	if n := s.pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("%d connections are still checked out after the panic", n)
	}
}

func expectChange(t *testing.T, ch <-chan Change, channel, id string) {
	t.Helper()
	select {
	case c := <-ch:
		if c.Channel != channel || c.FeedID != id {
			t.Fatalf("change = %+v, want %s/%s", c, channel, id)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no %s notification for %s", channel, id)
	}
}
