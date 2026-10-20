// Package stream serves the real-time entity feed over native WebSocket,
// replacing the Socket.IO gateway. Frames are JSON envelopes:
// {"type": string, "data": object, "ts": RFC3339}.
package stream

import (
	"encoding/json"
	"time"
)

// Inbound message types (client to server).
const (
	MsgViewportUpdate    = "viewport:update"
	MsgEntitySubscribe   = "entity:subscribe"
	MsgEntityUnsubscribe = "entity:unsubscribe"
	MsgPing              = "ping"
)

// Outbound message types (server to client). Entity and ageout names match
// the Socket.IO event names the Angular client already handled.
const (
	MsgConnectionAck      = "connection:ack"
	MsgViewportUpdated    = "viewport:updated"
	MsgEntitySubscribed   = "entity:subscribed"
	MsgEntityUnsubscribed = "entity:unsubscribed"
	MsgEntityBatch        = "entity:batch"
	MsgEntityDeleted      = "entity:deleted"
	MsgEntityStale        = "events.entity.stale"
	MsgEntityAgedOut      = "events.entity.agedout"
	MsgEntityRestored     = "events.entity.restored"
	MsgAlertNew           = "alert:new"
	MsgDensity            = "density:grid"
	MsgError              = "error"
	MsgPong               = "pong"
)

// Envelope is the wire frame in both directions.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
	TS   string          `json:"ts,omitempty"`
}

// Encode builds a frame with the current timestamp.
func Encode(msgType string, data interface{}) ([]byte, error) {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return json.Marshal(Envelope{Type: msgType, Data: raw, TS: time.Now().UTC().Format(time.RFC3339Nano)})
}

// Position is a WGS84 point with optional altitude in metres.
type Position struct {
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Altitude  *float64 `json:"altitude,omitempty"`
}

// Entity is the frontend entity shape carried by EntityEvent. Field names
// match apps/client/src/app/shared/models/entity.model.ts.
type Entity struct {
	ID                string                 `json:"id"`
	EntityType        string                 `json:"entityType"`
	Name              string                 `json:"name,omitempty"`
	Description       string                 `json:"description,omitempty"`
	Source            string                 `json:"source"`
	Classification    string                 `json:"classification"`
	Position          *Position              `json:"position,omitempty"`
	Heading           *float64               `json:"heading,omitempty"`
	SpeedKnots        *float64               `json:"speedKnots,omitempty"`
	Course            *float64               `json:"course,omitempty"`
	MilStd2525dSymbol string                 `json:"milStd2525dSymbol,omitempty"`
	Metadata          map[string]interface{} `json:"metadata"`
	Affiliations      []string               `json:"affiliations"`
	CreatedAt         string                 `json:"createdAt"`
	UpdatedAt         string                 `json:"updatedAt"`
	LastSeenAt        string                 `json:"lastSeenAt,omitempty"`
	TrackEnvironment  string                 `json:"trackEnvironment,omitempty"`
	CountryOfOrigin   string                 `json:"countryOfOrigin,omitempty"`
	PlatformData      map[string]interface{} `json:"platformData,omitempty"`
	OperationalStatus string                 `json:"operationalStatus,omitempty"`
	CircularError     *float64               `json:"circularError,omitempty"`
	// Kinematics carries {"velocity": {north, east, up}, "positionCovariance": {...}}
	// when known, matching the client's KinematicState shape.
	Kinematics map[string]interface{} `json:"kinematics,omitempty"`
	// Provenance and liveness from the fused entity.
	FeedID         string           `json:"feedId,omitempty"`
	LastFeedID     string           `json:"lastFeedId,omitempty"`
	AgeoutState    string           `json:"ageoutState,omitempty"`
	Identities     []EntityIdentity `json:"identities,omitempty"`
	LastObservedAt string           `json:"lastObservedAt,omitempty"`
	// Sources lists every feed that contributed to the fused entity.
	Sources []EntitySourceRef `json:"sources,omitempty"`
	// Exercise marks a simulated or training entity. Absent means live.
	Exercise bool `json:"exercise,omitempty"`
}

// EntitySourceRef is one feed's contribution to a fused entity.
type EntitySourceRef struct {
	FeedID         string `json:"feedId"`
	Source         string `json:"source,omitempty"`
	LastObservedAt string `json:"lastObservedAt,omitempty"`
}

// EntityEvent is what the client receives for created, updated, and deleted entities.
type EntityEvent struct {
	Type      string `json:"type"`
	Entity    Entity `json:"entity"`
	Timestamp string `json:"timestamp"`
	// Bulk and Count are set on a clear-all deletion.
	Bulk  bool `json:"bulk,omitempty"`
	Count int  `json:"count,omitempty"`
}

// ErrorData is the payload of an error frame.
type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ClientDensity is a feed's density grid as the map client draws it: the
// number of distinct objects per cell, sent while views are too large to
// store the objects one by one. Cells are [latitude, longitude, count]
// triples at the cell centre.
type ClientDensity struct {
	FeedID           string       `json:"feedId"`
	Source           string       `json:"source"`
	EntityType       string       `json:"entityType"`
	CellDegrees      float64      `json:"cellDegrees"`
	WindowSeconds    uint32       `json:"windowSeconds"`
	CapSquareDegrees float64      `json:"capSquareDegrees"`
	GeneratedAt      string       `json:"generatedAt"`
	Classification   string       `json:"classification"`
	Total            uint32       `json:"total"`
	Cells            [][3]float64 `json:"cells"`
}
