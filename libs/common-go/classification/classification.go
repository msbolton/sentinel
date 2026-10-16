// Package classification orders the sentinel.v1.Classification levels the
// same way sentinel.classification_rank does in the database, so every
// service applies a caller's clearance as the same ceiling.
package classification

import "strings"

// Levels, lowest first, spelled as stored on entities and alerts.
const (
	Unclassified = "UNCLASSIFIED"
	Confidential = "CONFIDENTIAL"
	Secret       = "SECRET"
	TopSecret    = "TOP_SECRET"
)

// Levels lists every level in ascending order.
var Levels = []string{Unclassified, Confidential, Secret, TopSecret}

// Rank orders levels: 0 for UNCLASSIFIED, unknown or empty, up to 3 for
// TOP_SECRET. Unknown values rank lowest so a bad value never grants access.
func Rank(level string) int {
	for i, l := range Levels {
		if l == strings.ToUpper(strings.TrimSpace(level)) {
			return i
		}
	}
	return 0
}

// Normalize returns the canonical spelling of level, or UNCLASSIFIED for
// anything unknown or empty.
func Normalize(level string) string {
	return Levels[Rank(level)]
}

// Valid reports whether level is one of the four levels, case-insensitively.
func Valid(level string) bool {
	u := strings.ToUpper(strings.TrimSpace(level))
	for _, l := range Levels {
		if l == u {
			return true
		}
	}
	return false
}

// Min returns the lower of two levels: the effective ceiling when a caller
// asks for one level while cleared for another.
func Min(a, b string) string {
	if Rank(a) < Rank(b) {
		return Normalize(a)
	}
	return Normalize(b)
}

// AtOrBelow lists the levels a caller cleared to ceiling may see.
func AtOrBelow(ceiling string) []string {
	return append([]string(nil), Levels[:Rank(ceiling)+1]...)
}

// Intersect keeps the requested levels that are at or below ceiling; with
// no request it returns everything at or below ceiling.
func Intersect(requested []string, ceiling string) []string {
	if len(requested) == 0 {
		return AtOrBelow(ceiling)
	}
	max := Rank(ceiling)
	var out []string
	for _, r := range requested {
		if Valid(r) && Rank(r) <= max {
			out = append(out, Normalize(r))
		}
	}
	if out == nil {
		// Everything asked for is above the ceiling: match nothing rather
		// than everything.
		return []string{"__NONE__"}
	}
	return out
}
