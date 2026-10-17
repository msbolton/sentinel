package celestrak

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Selection kinds, in the order requests are made.
const (
	kindGroup        = "group"
	kindCatalog      = "catnr"
	kindDesignator   = "intdes"
	kindName         = "name"
	kindSpecial      = "special"
	kindSupplemental = "supplemental"
)

// selection is one CelesTrak query the feed makes each refresh. Objects
// named individually (catalog number, designator, name) are pinned: they
// are emitted whether or not anyone is looking at them.
type selection struct {
	Kind   string
	Value  string
	Pinned bool
}

// Key identifies the selection in the cache and in health detail.
func (s selection) Key() string { return s.Kind + ":" + s.Value }

// URL is the request for this selection. Query names must be upper case
// for CelesTrak; FORMAT=json gives OMM records with six-digit catalog
// numbers and the epoch.
func (s selection) URL(base, supplemental string) string {
	switch s.Kind {
	case kindSupplemental:
		return fmt.Sprintf("%s?FILE=%s&FORMAT=json", supplemental, url.QueryEscape(s.Value))
	case kindCatalog:
		return fmt.Sprintf("%s?CATNR=%s&FORMAT=json", base, url.QueryEscape(s.Value))
	case kindDesignator:
		return fmt.Sprintf("%s?INTDES=%s&FORMAT=json", base, url.QueryEscape(s.Value))
	case kindName:
		return fmt.Sprintf("%s?NAME=%s&FORMAT=json", base, url.QueryEscape(s.Value))
	case kindSpecial:
		return fmt.Sprintf("%s?SPECIAL=%s&FORMAT=json", base, url.QueryEscape(strings.ToUpper(s.Value)))
	default:
		return fmt.Sprintf("%s?GROUP=%s&FORMAT=json", base, url.QueryEscape(s.Value))
	}
}

// selectionsOf lists the feed's selections, trimmed and without
// duplicates. A config that selects nothing watches the "active" group.
func selectionsOf(cfg Config) []selection {
	var out []selection
	seen := map[string]bool{}
	add := func(kind string, values []string, pinned bool) {
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			s := selection{Kind: kind, Value: v, Pinned: pinned}
			if seen[s.Key()] {
				continue
			}
			seen[s.Key()] = true
			out = append(out, s)
		}
	}
	add(kindGroup, cfg.Groups, false)
	nums := make([]string, 0, len(cfg.CatalogNumbers))
	for _, n := range cfg.CatalogNumbers {
		if n > 0 {
			nums = append(nums, strconv.Itoa(n))
		}
	}
	add(kindCatalog, nums, true)
	add(kindDesignator, cfg.Designators, true)
	add(kindName, cfg.Names, true)
	add(kindSpecial, cfg.Special, false)
	add(kindSupplemental, cfg.Supplemental, false)
	if len(out) == 0 {
		out = append(out, selection{Kind: kindGroup, Value: "active"})
	}
	return out
}
