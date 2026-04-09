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
