package registry

import (
	"context"
	"encoding/json"
	"fmt"
)

// Builtins are the feeds every deployment has. db/seed/seed.sql inserts the
// same rows; EnsureBuiltins repeats the insert at startup (ON CONFLICT DO
// NOTHING) for databases that skipped the seed.
var Builtins = []Feed{
	{
		ID: "opensky", Name: "OpenSky Network", Driver: "opensky", Builtin: true,
		Description: "Live ADS-B state vectors from the OpenSky Network, polled for the connected clients' viewports.",
		Config:      json.RawMessage(`{"intervalSec": 10, "viewportTtlSec": 90, "minPollSpacingSec": 3, "maxAreaSqDeg": 400, "maxIntervalSec": 60, "creditFloor": 200, "staleWarnSec": 120, "staleCriticalSec": 300}`),
		SecretRefs:  map[string]string{"clientId": "env:OPENSKY_CLIENT_ID", "clientSecret": "env:OPENSKY_CLIENT_SECRET"},
	},
	{
		ID: "adsb-lol", Name: "adsb.lol military", Driver: "adsblol", Builtin: true,
		Description: "Military aircraft from the adsb.lol community ADS-B network.",
		Config:      json.RawMessage(`{"intervalSec": 10, "staleWarnSec": 120, "staleCriticalSec": 300}`),
		SecretRefs:  map[string]string{},
	},
	{
		ID: "celestrak", Name: "CelesTrak satellites", Driver: "celestrak", Builtin: true,
		Description: "Satellite positions propagated with SGP4 from CelesTrak two-line elements.",
		Config:      json.RawMessage(`{"groups": ["active"], "tleRefreshHours": 6, "propagationIntervalSec": 60, "staleWarnSec": 600, "staleCriticalSec": 900}`),
		SecretRefs:  map[string]string{},
	},
}

// EnsureBuiltins inserts any missing built-in feed. Feeds named in
// enabledIDs are inserted enabled; an existing row is never changed.
func (s *Store) EnsureBuiltins(ctx context.Context, enabledIDs []string) error {
	enabled := map[string]bool{}
	for _, id := range enabledIDs {
		enabled[id] = true
	}
	for _, b := range Builtins {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO sentinel.feeds (id, name, description, driver, config, secret_refs, enabled, builtin)
			VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
			ON CONFLICT (id) DO NOTHING`,
			b.ID, b.Name, b.Description, b.Driver, b.Config, b.SecretRefs, enabled[b.ID])
		if err != nil {
			return fmt.Errorf("ensuring builtin feed %s: %w", b.ID, err)
		}
	}
	return nil
}
