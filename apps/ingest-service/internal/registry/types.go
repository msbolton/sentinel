// Package registry is the ingest service's view of the feed tables: the
// desired state operators write through the management API, the observed
// state the running replica reports, and the area-of-interest leases the
// API gateway pushes. Every write notifies listeners through Postgres
// LISTEN/NOTIFY so replicas reconcile without polling.
package registry

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/sentinel/ingest-service/internal/source/driver"
)

// Notification channels.
const (
	ChannelFeeds = "sentinel_feeds"
	ChannelAreas = "sentinel_feed_areas"
)

// Feed states reported in feed_status.state.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateIdle     = "idle"
	StateDegraded = "degraded"
	StateError    = "error"
	// StateUnassigned is rendered, never stored: the owning replica stopped
	// reporting and no other replica has taken the feed yet.
	StateUnassigned = "unassigned"
)

// Health values reported in feed_status.health.
const (
	HealthHealthy  = "healthy"
	HealthWarn     = "warn"
	HealthCritical = "critical"
	HealthUnknown  = "unknown"
)

var (
	ErrNotFound = errors.New("feed not found")
	ErrConflict = errors.New("feed already exists")
)

// Feed is one row of sentinel.feeds plus its status row when present.
type Feed struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Driver      string            `json:"driver"`
	Config      json.RawMessage   `json:"config"`
	SecretRefs  map[string]string `json:"secretRefs"`
	Enabled     bool              `json:"enabled"`
	Builtin     bool              `json:"builtin"`
	Version     int               `json:"version"`
	CreatedBy   string            `json:"createdBy"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
	Status      *Status           `json:"status,omitempty"`
}

// Status is the observed state written by the replica that runs the feed.
type Status struct {
	FeedID         string          `json:"-"`
	Owner          string          `json:"owner"`
	State          string          `json:"state"`
	Health         string          `json:"health"`
	LastError      string          `json:"lastError,omitempty"`
	LastMessageAt  *time.Time      `json:"lastMessageAt"`
	LastSuccessAt  *time.Time      `json:"lastSuccessAt"`
	MessagesTotal  int64           `json:"messagesTotal"`
	EntitiesLast   int             `json:"entitiesLast"`
	ErrorsTotal    int64           `json:"errorsTotal"`
	AppliedVersion int             `json:"appliedVersion"`
	Detail         json.RawMessage `json:"detail"`
	UpdatedAt      *time.Time      `json:"updatedAt"`
	// FencedBy and FencingToken, when set, make the write conditional on
	// the replica still holding the feed's lease with that token.
	FencedBy     string `json:"-"`
	FencingToken int64  `json:"-"`
}

// Box is a latitude/longitude box that does not cross the antimeridian.
type Box struct {
	North float64 `json:"north"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	West  float64 `json:"west"`
}

// Area is the area of interest pushed for a feed.
type Area struct {
	FeedID    string     `json:"feedId"`
	Active    bool       `json:"active"`
	Clients   int        `json:"clients"`
	Boxes     []Box      `json:"boxes"`
	ExpiresAt *time.Time `json:"expiresAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// Expired reports whether the lease has lapsed.
func (a Area) Expired(now time.Time) bool {
	return a.ExpiresAt != nil && now.After(*a.ExpiresAt)
}

// Change is one LISTEN/NOTIFY event.
type Change struct {
	Channel string
	FeedID  string
}

// CommonOptions are the runtime keys every driver's config may carry.
type CommonOptions struct {
	StaleWarnSec     int    `json:"staleWarnSec"`
	StaleCriticalSec int    `json:"staleCriticalSec"`
	Backpressure     string `json:"backpressure"`
	PinnedReplica    string `json:"pinnedReplica"`
	RawPayload       string `json:"rawPayload"`
	// Classification is the default level stamped on this feed's
	// observations (bare sentinel.v1.Classification name; "" = UNCLASSIFIED).
	Classification string `json:"classification"`
	// Direction narrows a "both" driver to one way; "" keeps the driver's.
	Direction string `json:"direction"`
	// Policy is the outbound sharing policy (entity types, feeds, boxes,
	// classification ceiling, alerts); only meaningful for out|both.
	Policy json.RawMessage `json:"policy"`
	// Outbound tunes the sink pipeline (queue, rate, lag, grace).
	Outbound json.RawMessage `json:"outbound"`
}

// Direction is the feed's effective direction given its driver's: the
// config may narrow "both" to "in" or "out" but never widen.
func (f Feed) Direction(driverDir driver.Direction) driver.Direction {
	want := driver.Direction(f.Common().Direction)
	drv := driverDir.Normalize()
	if want == "" || want == drv {
		return drv
	}
	if drv == driver.DirectionBoth && (want == driver.DirectionIn || want == driver.DirectionOut) {
		return want
	}
	return drv
}

// Common decodes the common options out of a feed's config.
func (f Feed) Common() CommonOptions {
	var c CommonOptions
	_ = json.Unmarshal(f.Config, &c)
	return c
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidSlug reports whether id is an acceptable feed id.
func ValidSlug(id string) bool { return slugRe.MatchString(id) }

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify derives a feed id from a display name: lower-case, runs of
// anything else collapsed to one dash, trimmed to the slug length.
func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugStrip.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}
