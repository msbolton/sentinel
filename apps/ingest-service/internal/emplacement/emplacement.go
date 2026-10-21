// Package emplacement holds the places observations come from: radar sites,
// masts, ships' radars, field observers. Users manage them through the
// management API; each ingest replica keeps them in memory (Cache) for the
// drivers that report as them.
package emplacement

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	MobilityFixed  = "FIXED"
	MobilityMobile = "MOBILE"

	AzimuthTrueNorth = "TRUE_NORTH"
	AzimuthPlatform  = "PLATFORM"

	// Channel is the NOTIFY channel every write announces itself on, with
	// the payload {"id": "<uuid>", "op": "upsert"|"delete"}.
	Channel = "sentinel_emplacements"
)

// SenderPrefix starts the createdBy of an emplacement a sender made over
// gRPC (UpsertEmplacement): "ingest-token:<token id>".
const SenderPrefix = "ingest-token:"

// SensorTypes are the bare sentinel.v1.SensorType names an emplacement may have.
var SensorTypes = []string{"RADAR", "EO_IR", "RF", "ACOUSTIC", "AIS_RECEIVER", "ADSB_RECEIVER", "HUMAN", "OTHER"}

var (
	ErrNotFound        = errors.New("emplacement not found")
	ErrVersionConflict = errors.New("emplacement changed since it was read")
	ErrNameTaken       = errors.New("an emplacement with this name already exists")
)

// SenderManaged reports whether a sender made e, rather than a person.
func (e Emplacement) SenderManaged() bool { return strings.HasPrefix(e.CreatedBy, SenderPrefix) }

// Point is a WGS84 position in degrees.
type Point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Coverage is the volume an emplacement can see around its boresight.
// AzimuthWidthDeg 360 is omni. All zero means no coverage is known.
type Coverage struct {
	MinRangeM       float64  `json:"minRangeM"`
	MaxRangeM       float64  `json:"maxRangeM"`
	AzimuthWidthDeg float64  `json:"azimuthWidthDeg"`
	ElevationMinDeg *float64 `json:"elevationMinDeg,omitempty"`
	ElevationMaxDeg *float64 `json:"elevationMaxDeg,omitempty"`
}

// Empty reports whether no coverage was given: nothing is drawn or checked.
func (c Coverage) Empty() bool {
	return c.MinRangeM == 0 && c.MaxRangeM == 0 && c.AzimuthWidthDeg == 0 && c.ElevationMinDeg == nil && c.ElevationMaxDeg == nil
}

// Sigma is one standard deviation of each polar measurement, used when a
// report carries none of its own.
type Sigma struct {
	RangeM       *float64 `json:"rangeM,omitempty"`
	AzimuthDeg   *float64 `json:"azimuthDeg,omitempty"`
	ElevationDeg *float64 `json:"elevationDeg,omitempty"`
}

// Emplacement is one stored emplacement, as the API returns it.
type Emplacement struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	Mobility         string     `json:"mobility"`
	SensorType       string     `json:"sensorType"`
	Position         *Point     `json:"position"`
	Altitude         *float64   `json:"altitude"`
	BoresightDeg     float64    `json:"boresightDeg"`
	AzimuthReference string     `json:"azimuthReference"`
	Coverage         Coverage   `json:"coverage"`
	DefaultSigma     Sigma      `json:"defaultSigma"`
	FeedID           *string    `json:"feedId"`
	Classification   string     `json:"classification"`
	Exercise         bool       `json:"exercise"`
	Active           bool       `json:"active"`
	LastReportAt     *time.Time `json:"lastReportAt"`
	// HeartbeatIntervalSec and LastHeartbeatAt are what the row last recorded
	// of the emplacement's heartbeats (the grpc driver writes them, throttled,
	// with the server's receive time); null until the first heartbeat and
	// after a change of connection.
	HeartbeatIntervalSec *int       `json:"heartbeatIntervalSec"`
	LastHeartbeatAt      *time.Time `json:"lastHeartbeatAt"`
	// Heartbeat and SilentSince are computed for the API from the owning
	// replica's status (Handler.Liveness), and never stored. Heartbeat is
	// empty for an emplacement whose connection does not heartbeat it, or
	// that has no connection; SilentSince is set while it is offline.
	Heartbeat   HeartbeatState `json:"heartbeat,omitempty"`
	SilentSince *time.Time     `json:"silentSince,omitempty"`
	CreatedBy   string         `json:"createdBy"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	Version     int            `json:"version"`
}

// Input is a create or PATCH body. A nil field is left as it is; FeedID ""
// unbinds the emplacement from its connection.
type Input struct {
	Name             *string   `json:"name"`
	Description      *string   `json:"description"`
	Mobility         *string   `json:"mobility"`
	SensorType       *string   `json:"sensorType"`
	Position         *Point    `json:"position"`
	Altitude         *float64  `json:"altitude"`
	BoresightDeg     *float64  `json:"boresightDeg"`
	AzimuthReference *string   `json:"azimuthReference"`
	Coverage         *Coverage `json:"coverage"`
	DefaultSigma     *Sigma    `json:"defaultSigma"`
	FeedID           *string   `json:"feedId"`
	Classification   *string   `json:"classification"`
	Exercise         *bool     `json:"exercise"`
	Active           *bool     `json:"active"`
	// Read-only fields a client may send back from a GET: accepted, ignored.
	Heartbeat            json.RawMessage `json:"heartbeat"`
	SilentSince          json.RawMessage `json:"silentSince"`
	LastHeartbeatAt      json.RawMessage `json:"lastHeartbeatAt"`
	HeartbeatIntervalSec json.RawMessage `json:"heartbeatIntervalSec"`
}

// New is what a create starts from before its input is applied.
func New() Emplacement {
	return Emplacement{AzimuthReference: AzimuthTrueNorth, Classification: "UNCLASSIFIED", Active: true}
}

// Apply copies the fields the input sets onto e. Enum-like values are
// upper-cased and names trimmed; Validate judges the result.
func (in Input) Apply(e *Emplacement) {
	upper := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
	if in.Name != nil {
		e.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		e.Description = *in.Description
	}
	if in.Mobility != nil {
		e.Mobility = upper(*in.Mobility)
	}
	if in.SensorType != nil {
		e.SensorType = upper(*in.SensorType)
	}
	if in.Position != nil {
		p := *in.Position
		e.Position = &p
	}
	if in.Altitude != nil {
		a := *in.Altitude
		e.Altitude = &a
	}
	if in.BoresightDeg != nil {
		e.BoresightDeg = *in.BoresightDeg
	}
	if in.AzimuthReference != nil {
		e.AzimuthReference = upper(*in.AzimuthReference)
	}
	if in.Coverage != nil {
		e.Coverage = *in.Coverage
	}
	if in.DefaultSigma != nil {
		e.DefaultSigma = *in.DefaultSigma
	}
	if in.FeedID != nil {
		if id := strings.TrimSpace(*in.FeedID); id == "" {
			e.FeedID = nil
		} else {
			e.FeedID = &id
		}
	}
	if in.Classification != nil {
		e.Classification = upper(*in.Classification)
	}
	if in.Exercise != nil {
		e.Exercise = *in.Exercise
	}
	if in.Active != nil {
		e.Active = *in.Active
	}
}
