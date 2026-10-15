package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/track-service/internal/models"
	"github.com/sentinel/track-service/internal/store"
)

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
	mux.HandleFunc("GET /api/v1/tracks/{entityId}", h.getTrackHistory)
	mux.HandleFunc("GET /api/v1/tracks/{entityId}/latest", h.getLatestPosition)
	mux.HandleFunc("GET /api/v1/tracks/{entityId}/segments", h.getTrackSegments)
	mux.HandleFunc("POST /api/v1/tracks/{entityId}/replay", h.replayTrack)
}

func (h *TrackHandler) getTrackHistory(w http.ResponseWriter, r *http.Request) {
	entityID := r.PathValue("entityId")

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
}

// parseTimelineParams reads startTime, endTime (RFC3339) and buckets. Missing
// times default to the last 24 hours; buckets defaults to 100 and is clamped.
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
	return p, nil
}

func (h *TrackHandler) getTimeline(w http.ResponseWriter, r *http.Request) {
	p, err := parseTimelineParams(r)
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	buckets, err := h.store.GetTimeline(r.Context(), p.Start, p.End, p.Buckets)
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "failed to query timeline", h.logger, err)
		return
	}
	httputil.JSON(w, http.StatusOK, buckets)
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
