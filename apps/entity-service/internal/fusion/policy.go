package fusion

import "sort"

// Dedupe keeps, per canonical key, the report that should move the entity:
// the newest source timestamp, ties broken by source priority, then by
// arrival order. Every other report with a position is marked DUPLICATE.
// Reports without a valid position are left as they are (they never move
// the entity). The result keeps the input order.
func Dedupe(reports []*Report) []*Report {
	winner := map[Key]int{}
	for i, r := range reports {
		if !r.Valid {
			continue
		}
		w, ok := winner[r.Key]
		if !ok || newer(r, reports[w]) {
			winner[r.Key] = i
		}
	}
	for i, r := range reports {
		if !r.Valid {
			continue
		}
		if winner[r.Key] != i {
			r.Valid = false
			r.Reject = RejectDuplicate
		}
	}
	return reports
}

// newer reports whether a should replace b as the batch winner.
func newer(a, b *Report) bool {
	if !a.ObservedAt.Equal(b.ObservedAt) {
		return a.ObservedAt.After(b.ObservedAt)
	}
	return a.Priority > b.Priority
}

// Keys returns the distinct canonical keys in first-seen order.
func Keys(reports []*Report) []Key {
	seen := map[Key]bool{}
	var out []Key
	for _, r := range reports {
		if !seen[r.Key] {
			seen[r.Key] = true
			out = append(out, r.Key)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return false })
	return out
}
