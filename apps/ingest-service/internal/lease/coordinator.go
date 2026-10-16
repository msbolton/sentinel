// Package lease assigns every feed to exactly one ingest replica through
// rows in Postgres. A replica heartbeats its own ingest_replicas row; the
// live set is the unexpired rows. A feed's preferred owner is chosen by
// rendezvous hashing over the live set (or pinned by configuration), and
// the owner holds a feed_leases row it renews before expiry. Every
// acquisition bumps the fencing token, which the owner stamps on the
// observations and status rows it writes, so a replica that lost its
// lease without noticing cannot overwrite the new owner's work.
package lease

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options tune the coordinator. Zero values take the defaults.
type Options struct {
	TTL time.Duration // lease and replica expiry (15 s)
}

func (o Options) withDefaults() Options {
	if o.TTL <= 0 {
		o.TTL = 15 * time.Second
	}
	return o
}

// Coordinator is one replica's view of the lease tables.
type Coordinator struct {
	pool    *pgxpool.Pool
	replica string
	opts    Options
}

// New builds a coordinator for this replica.
func New(pool *pgxpool.Pool, replicaID string, opts Options) *Coordinator {
	return &Coordinator{pool: pool, replica: replicaID, opts: opts.withDefaults()}
}

// ReplicaID returns this replica's id.
func (c *Coordinator) ReplicaID() string { return c.replica }

// TTL returns the lease duration.
func (c *Coordinator) TTL() time.Duration { return c.opts.TTL }

// Heartbeat refreshes this replica's row and prunes long-dead replicas.
func (c *Coordinator) Heartbeat(ctx context.Context) error {
	_, err := c.pool.Exec(ctx, `
		INSERT INTO sentinel.ingest_replicas (replica_id, started_at, heartbeat_at, expires_at)
		VALUES ($1, NOW(), NOW(), NOW() + $2::interval)
		ON CONFLICT (replica_id) DO UPDATE SET heartbeat_at = NOW(), expires_at = NOW() + $2::interval`,
		c.replica, c.opts.TTL)
	if err != nil {
		return fmt.Errorf("replica heartbeat: %w", err)
	}
	_, _ = c.pool.Exec(ctx, `DELETE FROM sentinel.ingest_replicas WHERE expires_at < NOW() - INTERVAL '1 hour'`)
	return nil
}

// Live returns the unexpired replica ids, sorted.
func (c *Coordinator) Live(ctx context.Context) ([]string, error) {
	rows, err := c.pool.Query(ctx, `SELECT replica_id FROM sentinel.ingest_replicas WHERE expires_at > NOW() ORDER BY replica_id`)
	if err != nil {
		return nil, fmt.Errorf("listing replicas: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Acquire takes the feed's lease when it is free, expired, or already
// this replica's. It returns the new fencing token, or ok false when
// another live replica holds it.
func (c *Coordinator) Acquire(ctx context.Context, feedID string) (int64, bool, error) {
	var token int64
	err := c.pool.QueryRow(ctx, `
		INSERT INTO sentinel.feed_leases (feed_id, owner_id, fencing_token, acquired_at, heartbeat_at, expires_at)
		VALUES ($1, $2, 1, NOW(), NOW(), NOW() + $3::interval)
		ON CONFLICT (feed_id) DO UPDATE SET
		  owner_id = EXCLUDED.owner_id, fencing_token = sentinel.feed_leases.fencing_token + 1,
		  acquired_at = NOW(), heartbeat_at = NOW(), expires_at = EXCLUDED.expires_at
		WHERE sentinel.feed_leases.expires_at < NOW() OR sentinel.feed_leases.owner_id = EXCLUDED.owner_id
		RETURNING fencing_token`, feedID, c.replica, c.opts.TTL).Scan(&token)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("acquiring lease for %s: %w", feedID, err)
	}
	return token, true, nil
}

// Renew extends a held lease. ok false means the lease was lost (expired
// and taken, or released): the caller must stop the feed at once.
func (c *Coordinator) Renew(ctx context.Context, feedID string, token int64) (bool, error) {
	tag, err := c.pool.Exec(ctx, `
		UPDATE sentinel.feed_leases SET heartbeat_at = NOW(), expires_at = NOW() + $4::interval
		 WHERE feed_id = $1 AND owner_id = $2 AND fencing_token = $3`, feedID, c.replica, token, c.opts.TTL)
	if err != nil {
		return false, fmt.Errorf("renewing lease for %s: %w", feedID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Release gives a held lease up so another replica can take it at once.
func (c *Coordinator) Release(ctx context.Context, feedID string, token int64) error {
	_, err := c.pool.Exec(ctx, `DELETE FROM sentinel.feed_leases WHERE feed_id = $1 AND owner_id = $2 AND fencing_token = $3`, feedID, c.replica, token)
	if err != nil {
		return fmt.Errorf("releasing lease for %s: %w", feedID, err)
	}
	return nil
}

// Leave releases every lease this replica holds and its replica row.
func (c *Coordinator) Leave(ctx context.Context) error {
	if _, err := c.pool.Exec(ctx, `DELETE FROM sentinel.feed_leases WHERE owner_id = $1`, c.replica); err != nil {
		return fmt.Errorf("releasing leases: %w", err)
	}
	if _, err := c.pool.Exec(ctx, `DELETE FROM sentinel.ingest_replicas WHERE replica_id = $1`, c.replica); err != nil {
		return fmt.Errorf("removing replica row: %w", err)
	}
	return nil
}

// Owner reports who holds a feed's lease and until when; ok false when
// nobody does (no row or expired).
func (c *Coordinator) Owner(ctx context.Context, feedID string) (owner string, token int64, expiresAt time.Time, ok bool, err error) {
	err = c.pool.QueryRow(ctx, `SELECT owner_id, fencing_token, expires_at FROM sentinel.feed_leases WHERE feed_id = $1 AND expires_at > NOW()`, feedID).Scan(&owner, &token, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, time.Time{}, false, nil
	}
	if err != nil {
		return "", 0, time.Time{}, false, fmt.Errorf("reading lease for %s: %w", feedID, err)
	}
	return owner, token, expiresAt, true, nil
}

// Preferred picks the owner for a feed: the pinned replica when set,
// else the live replica with the highest rendezvous hash. It returns ""
// when the live set is empty.
func Preferred(feedID, pinned string, live []string) string {
	if pinned != "" {
		return pinned
	}
	best, bestScore := "", uint64(0)
	for _, r := range live {
		h := fnv.New64a()
		_, _ = h.Write([]byte(feedID))
		_, _ = h.Write([]byte{'|'})
		_, _ = h.Write([]byte(r))
		if s := h.Sum64(); best == "" || s > bestScore || (s == bestScore && r < best) {
			best, bestScore = r, s
		}
	}
	return best
}
