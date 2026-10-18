// Package ids holds identifiers shared across services that must agree on
// the same literal strings.
package ids

// Feed ids are slugs: readable in URLs, Kafka keys, metric labels and logs,
// and stored as-is in entities."feedId", track_points."feedId" and
// feed_ageout_config."feedId".
const (
	// FeedIDManual marks entities created or edited over the REST API
	// rather than ingested from a feed.
	FeedIDManual = "manual"

	// Built-in feeds seeded by db/seed/seed.sql and the ingest registry.
	FeedIDOpenSky   = "opensky"
	FeedIDCelesTrak = "celestrak"
	FeedIDIntelSky  = "intelsky"
)
