package mgmt

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/decoder"
)

// feedDTO is the management API's feed resource.
type feedDTO struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Driver       string            `json:"driver"`
	Source       string            `json:"source"`
	Direction    string            `json:"direction"` // effective: config.direction narrowed from the driver's
	Builtin      bool              `json:"builtin"`
	Enabled      bool              `json:"enabled"`
	Version      int               `json:"version"`
	Config       json.RawMessage   `json:"config"`
	SecretRefs   map[string]string `json:"secretRefs"`
	Capabilities []string          `json:"capabilities"`
	CreatedBy    string            `json:"createdBy,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	Status       registry.Status   `json:"status"`
}

// toFeedDTO renders f for a caller. cleared says whether the caller's clearance
// reaches the feed's classification (clearedFor); when it does not, the decoder
// error texts in the status are left out. It is a parameter so that no route
// can render a status without deciding.
func toFeedDTO(f registry.Feed, catalog *drivers.Catalog, cleared bool) feedDTO {
	dto := feedDTO{
		ID: f.ID, Name: f.Name, Description: f.Description, Driver: f.Driver,
		Builtin: f.Builtin, Enabled: f.Enabled, Version: f.Version,
		Config: f.Config, SecretRefs: f.SecretRefs, Capabilities: []string{},
		CreatedBy: f.CreatedBy, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt,
	}
	if dto.SecretRefs == nil {
		dto.SecretRefs = map[string]string{}
	}
	if len(dto.Config) == 0 {
		dto.Config = json.RawMessage("{}")
	}
	if info, ok := catalog.Get(f.Driver); ok {
		dto.Source = info.Source
		dto.Capabilities = info.Capabilities
		dto.Direction = string(f.Direction(info.Dir()))
	}
	if dto.Source == "" {
		dto.Source = decoderSource(f.Config)
	}
	if f.Status != nil {
		dto.Status = *f.Status
	} else {
		dto.Status = registry.Status{State: registry.StateStopped, Health: registry.HealthUnknown}
	}
	if !cleared {
		dto.Status.Detail = withoutErrorTexts(dto.Status.Detail)
	}
	if len(dto.Status.Detail) == 0 {
		dto.Status.Detail = json.RawMessage("{}")
	}
	return dto
}

// decoderSource reports the EntitySource a stream feed's decoder writes.
func decoderSource(config json.RawMessage) string {
	var c struct {
		Decoder struct {
			Name string `json:"name"`
		} `json:"decoder"`
	}
	if json.Unmarshal(config, &c) != nil || c.Decoder.Name == "" {
		return ""
	}
	if def, ok := decoder.Lookup(c.Decoder.Name); ok {
		return def.Source
	}
	return ""
}

// createRequest is the body of POST /feeds.
type createRequest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Driver      string            `json:"driver"`
	Config      json.RawMessage   `json:"config"`
	SecretRefs  map[string]string `json:"secretRefs"`
	Enabled     *bool             `json:"enabled"`
}

// replaceRequest is the body of PUT /feeds/{id}: a full replacement of the
// mutable fields.
type replaceRequest struct {
	Name        *string           `json:"name"`
	Description *string           `json:"description"`
	Config      json.RawMessage   `json:"config"`
	SecretRefs  map[string]string `json:"secretRefs"`
	Enabled     *bool             `json:"enabled"`
}

// areaRequest is the body of PUT /feeds/{id}/area.
type areaRequest struct {
	Active  bool           `json:"active"`
	Clients int            `json:"clients"`
	Boxes   []registry.Box `json:"boxes"`
}

// areaResponse is returned by GET and PUT on the area endpoints. It is a
// superset of the former viewport response so existing tooling keeps working.
type areaResponse struct {
	FeedID               string         `json:"feedId"`
	Feed                 string         `json:"feed"`
	Enabled              bool           `json:"enabled"`
	Live                 bool           `json:"live"` // true when answered by the replica running the feed
	Active               bool           `json:"active"`
	Clients              int            `json:"clients"`
	Boxes                []registry.Box `json:"boxes"`
	AreaSqDeg            float64        `json:"areaSqDeg"`
	EffectiveIntervalSec int            `json:"effectiveIntervalSec"`
	Idle                 bool           `json:"idle"`
	CreditsRemaining     *int           `json:"creditsRemaining"`
	LowCredits           bool           `json:"lowCredits"`
	LastPollAt           *time.Time     `json:"lastPollAt"`
	NextPollInSec        int            `json:"nextPollInSec"`
	ExpiresAt            *time.Time     `json:"expiresAt"`
	UpdatedAt            *time.Time     `json:"updatedAt"`
}

func areaFromRow(f registry.Feed, a registry.Area) areaResponse {
	resp := areaResponse{
		FeedID: f.ID, Feed: f.ID, Enabled: f.Enabled,
		Active: a.Active, Clients: a.Clients, Boxes: a.Boxes,
		Idle: true, ExpiresAt: a.ExpiresAt,
	}
	if resp.Boxes == nil {
		resp.Boxes = []registry.Box{}
	}
	resp.AreaSqDeg = areaOf(a.Boxes)
	if !a.UpdatedAt.IsZero() {
		t := a.UpdatedAt
		resp.UpdatedAt = &t
	}
	return resp
}

func areaFromLive(f registry.Feed, a registry.Area, st aoi.Status) areaResponse {
	resp := areaFromRow(f, a)
	resp.Live = true
	resp.Active = st.Active
	resp.Clients = st.Clients
	resp.Boxes = make([]registry.Box, 0, len(st.Boxes))
	for _, b := range st.Boxes {
		resp.Boxes = append(resp.Boxes, registry.Box{North: b.North, South: b.South, East: b.East, West: b.West})
	}
	resp.AreaSqDeg = st.AreaSqDeg
	resp.EffectiveIntervalSec = int(st.EffectiveInterval / time.Second)
	resp.Idle = st.Idle
	resp.LowCredits = st.LowCredits
	resp.NextPollInSec = int((st.NextPollIn + time.Second - 1) / time.Second)
	if st.CreditsRemaining >= 0 {
		c := st.CreditsRemaining
		resp.CreditsRemaining = &c
	}
	if !st.LastPollAt.IsZero() {
		t := st.LastPollAt.UTC()
		resp.LastPollAt = &t
	}
	if !st.ExpiresAt.IsZero() {
		t := st.ExpiresAt.UTC()
		resp.ExpiresAt = &t
	}
	return resp
}

func areaOf(boxes []registry.Box) float64 {
	if len(boxes) == 0 {
		return 0
	}
	total := 0.0
	for _, b := range boxes {
		total += (b.North - b.South) * (b.East - b.West)
	}
	return total
}

type errorResponse struct {
	Error   string            `json:"error"`
	Details []drivers.Problem `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func writeProblems(w http.ResponseWriter, problems []drivers.Problem) {
	writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "invalid feed definition", Details: problems})
}
