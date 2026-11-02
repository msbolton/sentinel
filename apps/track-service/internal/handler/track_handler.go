package handler

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/go-common/middleware"
	"github.com/sentinel/track-service/internal/models"
	"github.com/sentinel/track-service/internal/store"
)

// ceilingOf is the caller's clearance as propagated by the gateway; a
// missing header fails closed to UNCLASSIFIED.
func ceilingOf(r *http.Request) string {
	return classification.Normalize(middleware.Classification(r.Context()))
}

// visible reports whether the caller may see the entity's track; an entity
// above the caller's clearance does not exist for them (404).
func (h *TrackHandler) visible(w http.ResponseWriter, r *http.Request, entityID string) bool {
	level, err := h.store.EntityClassification(r.Context(), entityID)
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "failed to check entity", h.logger, err)
		return false
	}
	if classification.Rank(level) > classification.Rank(ceilingOf(r)) {
		httputil.Error(w, http.StatusNotFound, "entity not found")
		return false
	}
	return true
}

// TrackHandler handles HTTP requests for track history.
type TrackHandler struct {
	store  *store.TrackStore
	logger *zap.Logger
}

// NewTrackHandler creates a new track handler.
func NewTrackHandler(store *store.TrackStore, logger *zap.Logger) *TrackHandler {
	return &TrackHandler{store: store, logger: logger}
}

// RegisterRoutes registers all track-related HTTP routes on the given mux.
func (h *TrackHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/tracks/timeline", h.getTimeline)
	mux.HandleFunc("GET /api/v1/tracks/replay", h.getReplay)
	mux.HandleFunc("GET /api/v1/tracks/{entityId}", h.getTrackHistory)
	mux.HandleFunc("GET /api/v1/tracks/{entityId}/latest", h.getLatestPosition)
	mux.HandleFunc("GET /api/v1/tracks/{entityId}/segments", h.getTrackSegments)
	mux.HandleFunc("POST /api/v1/tracks/{entityId}/replay", h.replayTrack)
}

func (h *TrackHandler) getTrackHistory(w http.ResponseWriter, r *http.Request) {
	entityID := r.PathValue("entityId")
	if !h.visible(w, r, entityID) {
		return
	}

	startTime := parseOptionalTime(r, "startTime")
	endTime := parseOptionalTime(r, "endTime")
	maxPoints := parseOptionalInt(r, "maxPoints")

	points, err := h.store.GetHistory(r.Context(), entityID, startTime, endTime, maxPoints)
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "failed to query track history", h.logger, err)
		return
	}

	if points == nil {
		points = []models.TrackPointResult{}
	}
	httputil.JSON(w, http.StatusOK, points)
}

func (h *TrackHandler) getLatestPosition(w http.ResponseWriter, r *http.Request) {
	entityID := r.PathValue("entityId")
	if !h.visible(w, r, entityID) {
		return
	}

	results, err := h.store.GetLatestPositions(r.Context(), []string{entityID})
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "failed to query latest position", h.logger, err)
		return
	}

	if len(results) == 0 {
		httputil.JSON(w, http.StatusOK, nil)
		return
	}

	httputil.JSON(w, http.StatusOK, results[0])
}

func (h *TrackHandler) getTrackSegments(w http.ResponseWriter, r *http.Request) {
	entityID := r.PathValue("entityId")
	if !h.visible(w, r, entityID) {
		return
	}

	startTime := parseOptionalTime(r, "startTime")
	endTime := parseOptionalTime(r, "endTime")

	points, err := h.store.GetHistory(r.Context(), entityID, startTime, endTime, nil)
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "failed to query track history", h.logger, err)
		return
	}

	segments := buildSegments(points)
	httputil.JSON(w, http.StatusOK, segments)
}

func (h *TrackHandler) replayTrack(w http.ResponseWriter, r *http.Request) {
	entityID := r.PathValue("entityId")
	if !h.visible(w, r, entityID) {
		return
	}
	channelID := fmt.Sprintf("track-replay-%s-%d", entityID, time.Now().UnixMilli())

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"channelId":       channelID,
		"entityId":        entityID,
		"wsUrl":           "/ws/track-replay/" + channelID,
		"message":         fmt.Sprintf("Subscribe to WebSocket channel '%s' to receive replay events", channelID),
	})
}

// Timeline defaults and limits.
const (
	defaultTimelineWindow  = 24 * time.Hour
	defaultTimelineBuckets = 100
	maxTimelineBuckets     = 1000
)

// timelineParams is the parsed query for GET /api/v1/tracks/timeline.
type timelineParams struct {
	Start   time.Time
	End     time.Time
	Buckets int
	Box     *models.BBox // nil: the whole world
}

// parseTimelineParams reads startTime, endTime (RFC3339), buckets and an
// optional bbox. Missing times default to the last 24 hours; buckets defaults
// to 100 and is clamped.
func parseTimelineParams(r *http.Request) (timelineParams, error) {
	p := timelineParams{Buckets: defaultTimelineBuckets}
	q := r.URL.Query()

	if v := q.Get("endTime"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return p, fmt.Errorf("endTime must be an RFC3339 timestamp")
		}
		p.End = t
	} else {
		p.End = time.Now().UTC()
	}
	if v := q.Get("startTime"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return p, fmt.Errorf("startTime must be an RFC3339 timestamp")
		}
		p.Start = t
	} else {
		p.Start = p.End.Add(-defaultTimelineWindow)
	}
	if !p.Start.Before(p.End) {
		return p, fmt.Errorf("startTime must be before endTime")
	}
	if v := q.Get("buckets"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, fmt.Errorf("buckets must be a positive integer")
		}
		if n > maxTimelineBuckets {
			n = maxTimelineBuckets
		}
		p.Buckets = n
	}
	if v := q.Get("bbox"); v != "" {
		box, err := parseBBox(v)
		if err != nil {
			return p, err
		}
		p.Box = &box
	}
	return p, nil
}

func (h *TrackHandler) getTimeline(w http.ResponseWriter, r *http.Request) {
	p, err := parseTimelineParams(r)
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	buckets, err := h.store.GetTimeline(r.Context(), p.Start, p.End, p.Buckets, ceilingOf(r), p.Box)
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "failed to query timeline", h.logger, err)
		return
	}
	httputil.JSON(w, http.StatusOK, buckets)
}

// Replay limits.
const (
	maxReplayWindow       = 7 * 24 * time.Hour
	defaultReplayEntities = 500
	maxReplayEntities     = 1000
	defaultReplayPoints   = 240
	maxReplayPoints       = 1000
)

// replayParams is the parsed query for GET /api/v1/tracks/replay.
type replayParams struct {
	Start, End  time.Time
	Box         models.BBox
	MaxEntities int
	MaxPoints   int
}

// parseBBox reads "west,south,east,north": four finite degrees with longitude
// in [-180, 180], latitude in [-90, 90] and south below north. West greater
// than east is a box across the antimeridian.
func parseBBox(v string) (models.BBox, error) {
	var b models.BBox
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return b, fmt.Errorf("bbox must be west,south,east,north")
	}
	var f [4]float64
	for i, part := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return b, fmt.Errorf("bbox must be four finite numbers: west,south,east,north")
		}
		f[i] = n
	}
	b = models.BBox{West: f[0], South: f[1], East: f[2], North: f[3]}
	if b.West < -180 || b.West > 180 || b.East < -180 || b.East > 180 {
		return models.BBox{}, fmt.Errorf("bbox longitudes must be within [-180, 180]")
	}
	if b.South < -90 || b.South > 90 || b.North < -90 || b.North > 90 {
		return models.BBox{}, fmt.Errorf("bbox latitudes must be within [-90, 90]")
	}
	if b.South >= b.North {
		return models.BBox{}, fmt.Errorf("bbox south must be below north")
	}
	return b, nil
}

// parseReplayLimit reads a positive integer query value, defaulting when it is
// absent and clamping to ceiling when it is larger.
func parseReplayLimit(r *http.Request, key string, def, ceiling int) (int, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	if n > ceiling {
		n = ceiling
	}
	return n, nil
}

// parseReplayParams reads the required startTime, endTime (RFC3339, at most
// seven days apart) and bbox, and the optional maxEntities and maxPoints.
func parseReplayParams(r *http.Request) (replayParams, error) {
	var p replayParams
	q := r.URL.Query()

	for _, f := range []struct {
		key string
		dst *time.Time
	}{{"startTime", &p.Start}, {"endTime", &p.End}} {
		v := q.Get(f.key)
		if v == "" {
			return p, fmt.Errorf("%s is required", f.key)
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return p, fmt.Errorf("%s must be an RFC3339 timestamp", f.key)
		}
		*f.dst = t
	}
	if !p.Start.Before(p.End) {
		return p, fmt.Errorf("startTime must be before endTime")
	}
	if p.End.Sub(p.Start) > maxReplayWindow {
		return p, fmt.Errorf("the replay window must not exceed 7 days")
	}

	v := q.Get("bbox")
	if v == "" {
		return p, fmt.Errorf("bbox is required")
	}
	box, err := parseBBox(v)
	if err != nil {
		return p, err
	}
	p.Box = box

	if p.MaxEntities, err = parseReplayLimit(r, "maxEntities", defaultReplayEntities, maxReplayEntities); err != nil {
		return p, err
	}
	if p.MaxPoints, err = parseReplayLimit(r, "maxPoints", defaultReplayPoints, maxReplayPoints); err != nil {
		return p, err
	}
	return p, nil
}

func (h *TrackHandler) getReplay(w http.ResponseWriter, r *http.Request) {
	p, err := parseReplayParams(r)
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	entities, truncated, err := h.store.GetReplay(r.Context(), store.ReplayQuery{
		Start: p.Start, End: p.End, Box: p.Box,
		MaxEntities: p.MaxEntities, MaxPoints: p.MaxPoints,
		Ceiling: ceilingOf(r),
	})
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "failed to query replay", h.logger, err)
		return
	}
	if entities == nil {
		entities = []models.ReplayEntity{}
	}
	httputil.JSON(w, http.StatusOK, models.ReplayResponse{
		StartTime: p.Start, EndTime: p.End, Entities: entities, Truncated: truncated,
	})
}

const segmentGapMs = 30 * 60 * 1000 // 30 minutes

func buildSegments(points []models.TrackPointResult) []models.TrackSegment {
	if len(points) == 0 {
		return []models.TrackSegment{}
	}

	var segments []models.TrackSegment
	current := []models.TrackPointResult{points[0]}

	for i := 1; i < len(points); i++ {
		prevMs := points[i-1].Timestamp.UnixMilli()
		currMs := points[i].Timestamp.UnixMilli()

		if currMs-prevMs > segmentGapMs {
			segments = append(segments, models.TrackSegment{
				StartTime: current[0].Timestamp,
				EndTime:   current[len(current)-1].Timestamp,
				Points:    current,
			})
			current = []models.TrackPointResult{points[i]}
		} else {
			current = append(current, points[i])
		}
	}

	segments = append(segments, models.TrackSegment{
		StartTime: current[0].Timestamp,
		EndTime:   current[len(current)-1].Timestamp,
		Points:    current,
	})

	return segments
}

func parseOptionalTime(r *http.Request, key string) *time.Time {
	val := r.URL.Query().Get(key)
	if val == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return nil
	}
	return &t
}

func parseOptionalInt(r *http.Request, key string) *int {
	val := r.URL.Query().Get(key)
	if val == "" {
		return nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return nil
	}
	return &n
}
