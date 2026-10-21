package export

import (
	"encoding/json"
	"io"
)

type feature struct {
	Type       string         `json:"type"`
	ID         string         `json:"id"`
	Geometry   geometry       `json:"geometry"`
	Properties map[string]any `json:"properties"`
}

type geometry struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"`
}

type featureCollection struct {
	Type     string    `json:"type"`
	Features []feature `json:"features"`
	Total    int       `json:"totalFeatures"`
}

// properties lists the attributes GIS consumers get for an entity.
func properties(e Entity) map[string]any {
	p := map[string]any{
		"name":           e.Name,
		"entityType":     e.EntityType,
		"classification": e.Classification,
		"source":         e.Source,
		"ageoutState":    e.AgeoutState,
	}
	set := func(k string, v any) {
		switch x := v.(type) {
		case *string:
			if x != nil && *x != "" {
				p[k] = *x
			}
		case *float64:
			if x != nil {
				p[k] = *x
			}
		}
	}
	set("description", e.Description)
	set("feedId", e.FeedID)
	set("lastFeedId", e.LastFeedID)
	set("heading", e.Heading)
	set("speedKnots", e.SpeedKnots)
	set("course", e.Course)
	set("altitude", e.Altitude)
	set("sidc", e.MilStd2525dSymbol)
	set("affiliation", e.Affiliation)
	set("trackEnvironment", e.TrackEnvironment)
	set("countryOfOrigin", e.CountryOfOrigin)
	set("lastObservedAt", e.LastObservedAt)
	set("lastSeenAt", e.LastSeenAt)
	if e.Exercise {
		p["exercise"] = true
	}
	return p
}

// WriteGeoJSON renders entities with a position as a FeatureCollection with
// [lon, lat, alt] points.
func WriteGeoJSON(w io.Writer, entities []Entity, total int) error {
	fc := featureCollection{Type: "FeatureCollection", Features: []feature{}, Total: total}
	for _, e := range entities {
		if e.Position == nil {
			continue
		}
		coords := []float64{e.Position.Lon, e.Position.Lat}
		if e.Altitude != nil {
			coords = append(coords, *e.Altitude)
		}
		fc.Features = append(fc.Features, feature{Type: "Feature", ID: e.ID, Geometry: geometry{Type: "Point", Coordinates: coords}, Properties: properties(e)})
	}
	enc := json.NewEncoder(w)
	return enc.Encode(fc)
}
