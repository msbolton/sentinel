package evaluator

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parsePolygon accepts a geofence polygon from rule config in either of the
// shapes used across the platform: [[lng, lat], ...] or
// [{"latitude": .., "longitude": ..}, ...]. It returns at least three points
// in [lng, lat] order, or ok=false.
func parsePolygon(raw interface{}) ([][]float64, bool) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var pairs [][]float64
	if err := json.Unmarshal(b, &pairs); err == nil {
		var out [][]float64
		for _, p := range pairs {
			if len(p) >= 2 {
				out = append(out, []float64{p[0], p[1]})
			}
		}
		if len(out) >= 3 {
			return out, true
		}
		return nil, false
	}
	var objs []struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
		Lat       *float64 `json:"lat"`
		Lng       *float64 `json:"lng"`
		Lon       *float64 `json:"lon"`
	}
	if err := json.Unmarshal(b, &objs); err != nil {
		return nil, false
	}
	var out [][]float64
	for _, o := range objs {
		lat, lng := o.Latitude, o.Longitude
		if lat == nil {
			lat = o.Lat
		}
		if lng == nil {
			if o.Lng != nil {
				lng = o.Lng
			} else {
				lng = o.Lon
			}
		}
		if lat != nil && lng != nil {
			out = append(out, []float64{*lng, *lat})
		}
	}
	if len(out) < 3 {
		return nil, false
	}
	return out, true
}

// polygonWKT renders a closed WKT polygon from [lng, lat] points.
func polygonWKT(coords [][]float64) string {
	if coords[0][0] != coords[len(coords)-1][0] || coords[0][1] != coords[len(coords)-1][1] {
		coords = append(coords, coords[0])
	}
	points := make([]string, len(coords))
	for i, c := range coords {
		points[i] = fmt.Sprintf("%f %f", c[0], c[1])
	}
	return fmt.Sprintf("POLYGON((%s))", strings.Join(points, ","))
}

// triggerMode reads the geofence trigger from config: an explicit "triggerOn"
// (ENTRY, EXIT, BOTH) wins; otherwise trigger_on_enter / trigger_on_exit
// booleans are honoured. The default is BOTH.
func triggerMode(config map[string]interface{}) string {
	if t, ok := config["triggerOn"].(string); ok && t != "" {
		return strings.ToUpper(t)
	}
	enter, hasEnter := config["trigger_on_enter"].(bool)
	exit, hasExit := config["trigger_on_exit"].(bool)
	switch {
	case !hasEnter && !hasExit:
		return "BOTH"
	case enter && !exit:
		return "ENTRY"
	case exit && !enter:
		return "EXIT"
	case enter && exit:
		return "BOTH"
	default:
		return "NONE"
	}
}

// geofenceAlertType returns the alert to raise for a containment result under
// the given trigger mode, or "" when nothing fires.
func geofenceAlertType(contains bool, mode string) string {
	switch {
	case contains && (mode == "ENTRY" || mode == "BOTH"):
		return "GEOFENCE_ENTRY"
	case !contains && (mode == "EXIT" || mode == "BOTH"):
		return "GEOFENCE_EXIT"
	}
	return ""
}

// evaluateSpeedRule applies maxSpeedKnots / minSpeedKnots thresholds from a
// rule config and returns whether the rule fires with a description.
func evaluateSpeedRule(entityID string, config map[string]interface{}, speedKnots float64) (bool, string) {
	if maxSpeed, ok := toFloat(config["maxSpeedKnots"]); ok && speedKnots > maxSpeed {
		return true, fmt.Sprintf("Entity %s speed %.1f kn exceeds max %.1f kn", entityID, speedKnots, maxSpeed)
	}
	if minSpeed, ok := toFloat(config["minSpeedKnots"]); ok && speedKnots < minSpeed {
		return true, fmt.Sprintf("Entity %s speed %.1f kn below min %.1f kn", entityID, speedKnots, minSpeed)
	}
	return false, ""
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}
