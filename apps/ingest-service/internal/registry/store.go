package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/emplacement"
)

// Store reads and writes the feed tables.
type Store struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// Open connects to Postgres.
func Open(ctx context.Context, databaseURL string, logger *zap.Logger) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Store{pool: pool, logger: logger}, nil
}

// NewWithPool wraps an existing pool (tests).
func NewWithPool(pool *pgxpool.Pool, logger *zap.Logger) *Store {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Store{pool: pool, logger: logger}
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Pool exposes the connection pool for the lease coordinator.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping checks the connection; used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

const feedColumns = `
	f.id, f.name, f.description, f.driver, f.config, f.secret_refs, f.enabled, f.builtin, f.version,
	COALESCE(f.created_by, ''), f.created_at, f.updated_at,
	s.feed_id IS NOT NULL,
	COALESCE(s.owner, ''), COALESCE(s.state, 'stopped'), COALESCE(s.health, 'unknown'), COALESCE(s.last_error, ''),
	s.last_message_at, s.last_success_at,
	COALESCE(s.messages_total, 0), COALESCE(s.entities_last, 0), COALESCE(s.errors_total, 0), COALESCE(s.applied_version, 0),
	COALESCE(s.detail, '{}'::jsonb), s.updated_at`

const feedFrom = ` FROM sentinel.feeds f LEFT JOIN sentinel.feed_status s ON s.feed_id = f.id`

func scanFeed(row pgx.Row) (Feed, error) {
	var (
		f         Feed
		st        Status
		hasStatus bool
	)
	err := row.Scan(
		&f.ID, &f.Name, &f.Description, &f.Driver, &f.Config, &f.SecretRefs, &f.Enabled, &f.Builtin, &f.Version,
		&f.CreatedBy, &f.CreatedAt, &f.UpdatedAt,
		&hasStatus,
		&st.Owner, &st.State, &st.Health, &st.LastError,
		&st.LastMessageAt, &st.LastSuccessAt,
		&st.MessagesTotal, &st.EntitiesLast, &st.ErrorsTotal, &st.AppliedVersion,
		&st.Detail, &st.UpdatedAt,
	)
	if err != nil {
		return Feed{}, err
	}
	if f.SecretRefs == nil {
		f.SecretRefs = map[string]string{}
	}
	if len(f.Config) == 0 {
		f.Config = json.RawMessage("{}")
	}
	st.FeedID = f.ID
	if len(st.Detail) == 0 {
		st.Detail = json.RawMessage("{}")
	}
	f.Status = &st
	_ = hasStatus
	return f, nil
}

// List returns every feed, built-ins first, with its status.
func (s *Store) List(ctx context.Context) ([]Feed, error) {
	rows, err := s.pool.Query(ctx, "SELECT"+feedColumns+feedFrom+" ORDER BY f.builtin DESC, f.created_at, f.id")
	if err != nil {
		return nil, fmt.Errorf("listing feeds: %w", err)
	}
	defer rows.Close()
	var out []Feed
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning feed: %w", err)
		}
		out = append(out, f)
	}
	if out == nil {
		out = []Feed{}
	}
	return out, rows.Err()
}

// Get returns one feed or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Feed, error) {
	f, err := scanFeed(s.pool.QueryRow(ctx, "SELECT"+feedColumns+feedFrom+" WHERE f.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Feed{}, ErrNotFound
	}
	if err != nil {
		return Feed{}, fmt.Errorf("getting feed %s: %w", id, err)
	}
	return f, nil
}

// Guard checks a feed about to be written against every feed in the registry
// and returns the problems that refuse the write. others holds every stored
// feed, f itself included when it is already stored, with only ID, Name,
// Driver, Config and Enabled set: its other fields are zero. A guarded write
// runs the guard under the advisory lock named by feedPortsLockKey.
type Guard func(f Feed, others []Feed) []Problem

// feedPortsLockKey names the transaction-level advisory lock a guarded write
// holds from its check to its commit, so that two connections cannot both be
// checked for the same port before either is stored. It is hashed with
// hashtext, as the analytics baseline job hashes its own key.
const feedPortsLockKey = "sentinel.ingest.feed-ports"

// Create inserts a feed with version 1 and returns the stored row.
func (s *Store) Create(ctx context.Context, f *Feed) error {
	return s.CreateGuarded(ctx, f, nil)
}

// CreateGuarded is Create once g has accepted the feed. g runs in the insert's
// transaction under the advisory lock on feed ports (see runGuard); its
// problems come back as a *ValidationError and nothing is stored. A nil g
// takes no lock.
func (s *Store) CreateGuarded(ctx context.Context, f *Feed, g Guard) error {
	if f.SecretRefs == nil {
		f.SecretRefs = map[string]string{}
	}
	if len(f.Config) == 0 {
		f.Config = json.RawMessage("{}")
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := runGuard(ctx, tx, *f, g); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO sentinel.feeds (id, name, description, driver, config, secret_refs, enabled, builtin, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''))`,
			f.ID, f.Name, f.Description, f.Driver, f.Config, f.SecretRefs, f.Enabled, f.Builtin, f.CreatedBy)
		if isUniqueViolation(err) {
			return ErrConflict
		}
		if err != nil {
			return fmt.Errorf("inserting feed %s: %w", f.ID, err)
		}
		return notify(ctx, tx, ChannelFeeds, f.ID)
	})
	if err != nil {
		return err
	}
	stored, err := s.Get(ctx, f.ID)
	if err != nil {
		return err
	}
	*f = stored
	return nil
}

// Update replaces the mutable columns, bumps the version, and returns the
// stored row. Driver and builtin are immutable.
func (s *Store) Update(ctx context.Context, f *Feed) error {
	return s.UpdateGuarded(ctx, f, nil)
}

// UpdateGuarded is Update once g has accepted the feed, checked and refused the
// way CreateGuarded does.
func (s *Store) UpdateGuarded(ctx context.Context, f *Feed, g Guard) error {
	if f.SecretRefs == nil {
		f.SecretRefs = map[string]string{}
	}
	if len(f.Config) == 0 {
		f.Config = json.RawMessage("{}")
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := runGuard(ctx, tx, *f, g); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE sentinel.feeds
			   SET name = $2, description = $3, config = $4, secret_refs = $5, enabled = $6,
			       version = version + 1, updated_at = NOW()
			 WHERE id = $1`,
			f.ID, f.Name, f.Description, f.Config, f.SecretRefs, f.Enabled)
		if err != nil {
			return fmt.Errorf("updating feed %s: %w", f.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return notify(ctx, tx, ChannelFeeds, f.ID)
	})
	if err != nil {
		return err
	}
	stored, err := s.Get(ctx, f.ID)
	if err != nil {
		return err
	}
	*f = stored
	return nil
}

// SetEnabled flips the desired state and bumps the version.
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) (Feed, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE sentinel.feeds SET enabled = $2, version = version + 1, updated_at = NOW()
			 WHERE id = $1 AND enabled <> $2`, id, enabled)
		if err != nil {
			return fmt.Errorf("toggling feed %s: %w", id, err)
		}
		if tag.RowsAffected() == 0 {
			// Either unknown or already in the desired state; Get decides.
			return nil
		}
		return notify(ctx, tx, ChannelFeeds, id)
	})
	if err != nil {
		return Feed{}, err
	}
	return s.Get(ctx, id)
}

// emplacementsChannel is the emplacement store's channel (emplacement.Channel;
// a test keeps the two equal).
const emplacementsChannel = "sentinel_emplacements"

// Delete removes a feed; feed_status, feed_areas, feed_leases and its ingest
// tokens cascade. Its emplacements are let go in the same transaction, each
// announced the way the emplacement store announces its own writes: the ones
// its senders made (createdBy "ingest-token:...") are soft-deleted, the rest
// unbound. The foreign key would only null feedId silently, and a cache that
// never hears of it keeps binding the emplacement to a feed that is gone, or
// to a new feed that takes its id.
func (s *Store) Delete(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE sentinel.emplacements SET
				"deletedAt" = CASE WHEN "createdBy" LIKE $2 THEN NOW() ELSE "deletedAt" END,
				"feedId" = NULL, version = version + 1, "updatedAt" = NOW()
			WHERE "feedId" = $1 AND "deletedAt" IS NULL
			RETURNING id::text, "deletedAt" IS NOT NULL`, id, emplacement.SenderPrefix+"%")
		if err != nil {
			return fmt.Errorf("letting go of feed %s's emplacements: %w", id, err)
		}
		type change struct {
			id      string
			deleted bool
		}
		var changes []change
		for rows.Next() {
			var c change
			if err := rows.Scan(&c.id, &c.deleted); err != nil {
				rows.Close()
				return err
			}
			changes = append(changes, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM sentinel.feeds WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting feed %s: %w", id, err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		for _, c := range changes {
			op := "upsert"
			if c.deleted {
				op = "delete"
			}
			payload, _ := json.Marshal(map[string]string{"id": c.id, "op": op})
			if err := notify(ctx, tx, emplacementsChannel, string(payload)); err != nil {
				return err
			}
		}
		return notify(ctx, tx, ChannelFeeds, id)
	})
}

// ErrFenced is returned when a fenced status write finds the lease gone.
var ErrFenced = errors.New("status write fenced: lease no longer held")

// UpsertStatus records the observed state of a feed. With FencedBy and
// FencingToken set, an existing row is only overwritten while that
// replica still holds the feed's lease with that token, so a replica that
// lost its lease cannot clobber the new owner's report.
func (s *Store) UpsertStatus(ctx context.Context, st Status) error {
	if len(st.Detail) == 0 {
		st.Detail = json.RawMessage("{}")
	}
	query := `
		INSERT INTO sentinel.feed_status
		  (feed_id, owner, state, health, last_error, last_message_at, last_success_at,
		   messages_total, entities_last, errors_total, applied_version, detail, updated_at)
		VALUES ($1, NULLIF($2, ''), $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, NOW())
		ON CONFLICT (feed_id) DO UPDATE SET
		  owner = EXCLUDED.owner, state = EXCLUDED.state, health = EXCLUDED.health,
		  last_error = EXCLUDED.last_error, last_message_at = EXCLUDED.last_message_at,
		  last_success_at = EXCLUDED.last_success_at, messages_total = EXCLUDED.messages_total,
		  entities_last = EXCLUDED.entities_last, errors_total = EXCLUDED.errors_total,
		  applied_version = EXCLUDED.applied_version, detail = EXCLUDED.detail, updated_at = NOW()`
	args := []any{st.FeedID, st.Owner, st.State, st.Health, st.LastError, st.LastMessageAt, st.LastSuccessAt,
		st.MessagesTotal, st.EntitiesLast, st.ErrorsTotal, st.AppliedVersion, st.Detail}
	if st.FencingToken > 0 {
		query += `
		WHERE EXISTS (SELECT 1 FROM sentinel.feed_leases l WHERE l.feed_id = EXCLUDED.feed_id AND l.owner_id = $13 AND l.fencing_token = $14)`
		args = append(args, st.FencedBy, st.FencingToken)
	}
	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("upserting status for %s: %w", st.FeedID, err)
	}
	if st.FencingToken > 0 && tag.RowsAffected() == 0 {
		return ErrFenced
	}
	return nil
}

// ReleaseStatuses marks every feed owned by replica as stopped, for shutdown.
func (s *Store) ReleaseStatuses(ctx context.Context, replica string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sentinel.feed_status SET owner = NULL, state = $2, health = $3, updated_at = NOW()
		 WHERE owner = $1`, replica, StateStopped, HealthUnknown)
	if err != nil {
		return fmt.Errorf("releasing statuses for %s: %w", replica, err)
	}
	return nil
}

// Replica is a live ingest replica and the role it plays: an ordinary
// replica, or a listener (with the address senders use) that is shared when it
// also takes ordinary feeds.
type Replica struct {
	ID       string `json:"id"`
	Listener bool   `json:"listener"`
	Shared   bool   `json:"shared"`
	Host     string `json:"host"`
}

// NoListenerError is shown on listening connections while no listener replica is live.
const NoListenerError = "no listener replica (run the ingest listener: INGEST_LISTENER=true)"

// NoOrdinaryReplicaError is shown on ordinary feeds while the only live replicas are dedicated listeners.
const NoOrdinaryReplicaError = "no ingest replica for this feed: the only live replicas are listeners, which run listening connections only (start ingest-service, or set INGEST_LISTENER_SHARED=true on the listener)"

// LiveReplicas lists the unexpired replicas with their roles, by id.
func (s *Store) LiveReplicas(ctx context.Context) ([]Replica, error) {
	rows, err := s.pool.Query(ctx, `SELECT replica_id, listener, listener_shared, COALESCE(listener_host, '') FROM sentinel.ingest_replicas WHERE expires_at > NOW() ORDER BY replica_id`)
	if err != nil {
		return nil, fmt.Errorf("listing live replicas: %w", err)
	}
	defer rows.Close()
	var out []Replica
	for rows.Next() {
		var r Replica
		if err := rows.Scan(&r.ID, &r.Listener, &r.Shared, &r.Host); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetArea returns the pushed area for a feed, or an inactive area when none
// has been pushed.
func (s *Store) GetArea(ctx context.Context, id string) (Area, error) {
	a := Area{FeedID: id, Boxes: []Box{}}
	err := s.pool.QueryRow(ctx, `
		SELECT active, clients, boxes, expires_at, updated_at FROM sentinel.feed_areas WHERE feed_id = $1`, id).
		Scan(&a.Active, &a.Clients, &a.Boxes, &a.ExpiresAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return Area{}, fmt.Errorf("getting area for %s: %w", id, err)
	}
	if a.Boxes == nil {
		a.Boxes = []Box{}
	}
	return a, nil
}

// SetArea stores the pushed area and notifies the owning replica.
func (s *Store) SetArea(ctx context.Context, a Area) error {
	if a.Boxes == nil {
		a.Boxes = []Box{}
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO sentinel.feed_areas (feed_id, active, clients, boxes, expires_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, NOW())
			ON CONFLICT (feed_id) DO UPDATE SET
			  active = EXCLUDED.active, clients = EXCLUDED.clients, boxes = EXCLUDED.boxes,
			  expires_at = EXCLUDED.expires_at, updated_at = NOW()`,
			a.FeedID, a.Active, a.Clients, a.Boxes, a.ExpiresAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return ErrNotFound
			}
			return fmt.Errorf("setting area for %s: %w", a.FeedID, err)
		}
		return notify(ctx, tx, ChannelAreas, a.FeedID)
	})
}

// Watch delivers change notifications until ctx is done. The connection is
// re-established after errors; callers keep a periodic poll as a fallback
// because notifications sent while disconnected are lost.
func (s *Store) Watch(ctx context.Context) <-chan Change {
	out := make(chan Change, 16)
	go func() {
		defer close(out)
		for ctx.Err() == nil {
			if err := s.listen(ctx, out); err != nil && ctx.Err() == nil {
				s.logger.Warn("feed watch disconnected; reconnecting", zap.Error(err))
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
		}
	}()
	return out
}

func (s *Store) listen(ctx context.Context, out chan<- Change) error {
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := pooled.Hijack() // a LISTENing connection must not go back to the pool
	defer conn.Close(context.Background())

	for _, ch := range []string{ChannelFeeds, ChannelAreas} {
		if _, err := conn.Exec(ctx, "LISTEN "+ch); err != nil {
			return err
		}
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		select {
		case out <- Change{Channel: n.Channel, FeedID: n.Payload}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// runGuard is the check of a guarded write, made inside its transaction: it
// takes the advisory lock on feed ports, reads every feed and runs g. The lock
// is held until the transaction ends. The transaction is READ COMMITTED (see
// runInTx), so the read takes its snapshot only once the lock is granted and
// sees what the writer that held the lock before committed. Problems refuse the
// write as a *ValidationError, which the management API maps to 422. A nil g
// checks nothing and takes no lock.
func runGuard(ctx context.Context, tx pgx.Tx, f Feed, g Guard) error {
	if g == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, feedPortsLockKey); err != nil {
		return fmt.Errorf("locking feed ports: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT id, name, driver, config, enabled FROM sentinel.feeds ORDER BY created_at, id`)
	if err != nil {
		return fmt.Errorf("reading feeds to check: %w", err)
	}
	defer rows.Close()
	var others []Feed
	for rows.Next() {
		var o Feed
		if err := rows.Scan(&o.ID, &o.Name, &o.Driver, &o.Config, &o.Enabled); err != nil {
			return fmt.Errorf("scanning feed to check: %w", err)
		}
		others = append(others, o)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading feeds to check: %w", err)
	}
	if problems := g(f, others); len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func notify(ctx context.Context, tx pgx.Tx, channel, payload string) error {
	if _, err := tx.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload); err != nil {
		return fmt.Errorf("notifying %s: %w", channel, err)
	}
	return nil
}

// txBeginner starts a transaction; *pgxpool.Pool is one, and tests use a fake.
type txBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return runInTx(ctx, s.pool, fn)
}

// runInTx runs fn in a transaction on db: committed when fn returns nil, rolled
// back when it returns an error or panics.
//
// Every store transaction is READ COMMITTED. That is the Postgres default, but
// it is pinned because runGuard depends on it: each statement takes a fresh
// snapshot, so the read after the lock wait sees the previous holder's commit.
// Under REPEATABLE READ the snapshot would be taken at the lock statement,
// before the wait, and the lock would protect nothing; under SERIALIZABLE the
// losing save would fail with a serialization error (40001).
func runInTx(ctx context.Context, db txBeginner, fn func(pgx.Tx) error) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	// Deferred so that a panic in fn cannot strand the transaction, and the port
	// lock it may hold, on a connection that never returns to the pool. A no-op
	// once Commit has run.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
