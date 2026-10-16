package stream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"
)

// Handler upgrades GET /ws/entities to a websocket and runs the client loops.
// Authentication is applied by the caller (auth.Middleware.RequireAuth).
type Handler struct {
	hub            *Hub
	logger         *zap.Logger
	originPatterns []string
	skipOrigin     bool
}

// NewHandler creates the websocket handler. allowedOrigin is the CORS origin
// setting: "*" disables the origin check, otherwise its host is allowed in
// addition to same-origin requests.
func NewHandler(hub *Hub, allowedOrigin string, logger *zap.Logger) *Handler {
	h := &Handler{hub: hub, logger: logger}
	switch {
	case allowedOrigin == "" || allowedOrigin == "*":
		h.skipOrigin = true
	default:
		for _, o := range strings.Split(allowedOrigin, ",") {
			o = strings.TrimSpace(o)
			if u, err := url.Parse(o); err == nil && u.Host != "" {
				h.originPatterns = append(h.originPatterns, u.Host)
			} else if o != "" {
				h.originPatterns = append(h.originPatterns, o)
			}
		}
	}
	return h
}

// RegisterRoutes mounts the websocket endpoint on mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ws/entities", h.ServeHTTP)
}

// ServeHTTP accepts the websocket upgrade.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:     h.originPatterns,
		InsecureSkipVerify: h.skipOrigin,
		CompressionMode:    websocket.CompressionContextTakeover,
	})
	if err != nil {
		h.logger.Debug("ws accept failed", zap.Error(err))
		return
	}

	client := newClient(newClientID(), h.hub, conn, h.logger)
	if u, ok := auth.UserFrom(r.Context()); ok {
		client.clearance = auth.ClassificationRank(u.ClassificationLevel)
	}
	h.hub.register(client)
	defer h.hub.unregister(client)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go client.writeLoop(ctx)
	client.sendTyped(MsgConnectionAck, map[string]interface{}{
		"clientId":   client.ID,
		"viewport":   GlobalBounds,
		"serverTime": time.Now().UTC().Format(time.RFC3339),
	})
	client.readLoop(ctx)
	client.close(websocket.StatusNormalClosure, "bye")
}

func newClientID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
