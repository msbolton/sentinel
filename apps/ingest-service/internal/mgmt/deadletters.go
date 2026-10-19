package mgmt

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/dlq"
)

const (
	defaultLetters = 20  // letters returned when limit is not given
	maxLetters     = 100 // most letters one request can ask for
)

// letterDTO is one dead letter in the response. The client is built against
// this shape: the fields are written in this order, payload and
// payloadEncoding only when the payload was asked for, everything else always.
type letterDTO struct {
	At      time.Time `json:"at"`
	Reason  string    `json:"reason"`
	Error   string    `json:"error"`
	Driver  string    `json:"driver"`
	Decoder string    `json:"decoder"`
	Sender  string    `json:"sender"`
	Size    int       `json:"size"`
	// Payload is the start of the payload as text when that is valid UTF-8, and
	// as base64 when it is not; PayloadEncoding says which.
	Payload          *string `json:"payload,omitempty"`
	PayloadEncoding  string  `json:"payloadEncoding,omitempty"`
	PayloadTruncated bool    `json:"payloadTruncated"`
	Partition        int32   `json:"partition"`
	Offset           int64   `json:"offset"`
}

// deadLettersResponse is the body of GET /feeds/{id}/deadletters.
type deadLettersResponse struct {
	Letters []letterDTO `json:"letters"`
	// Scanned is how many dead-letter messages were read to find them, whichever
	// feed they belong to. Partial means the read stopped at its time limit
	// before it had covered them all.
	Scanned int  `json:"scanned"`
	Partial bool `json:"partial"`
}

func toLetterDTO(l dlq.Letter, withPayload bool) letterDTO {
	dto := letterDTO{
		At: l.At.UTC(), Reason: l.Reason, Error: l.Error, Driver: l.Driver, Decoder: l.Decoder, Sender: l.Sender,
		Size: l.Size, PayloadTruncated: l.PayloadTruncated, Partition: l.Partition, Offset: l.Offset,
	}
	if withPayload {
		payload, encoding := string(l.Payload), "utf8"
		if !utf8.Valid(l.Payload) {
			payload, encoding = base64.StdEncoding.EncodeToString(l.Payload), "base64"
		}
		dto.Payload, dto.PayloadEncoding = &payload, encoding
	}
	return dto
}

// deadLetters serves a connection's most recent dead letters: the payloads its
// decoder refused, newest first, with why and from whom. Only an operator or
// admin whose clearance reaches the connection's classification may read them:
// a dead letter is undecoded, so the connection's classification is the only
// bound on what it holds. The gateway has no rule for GET on /feeds, so the
// check is made here, and before the topic is read.
func (h *Handler) deadLetters(w http.ResponseWriter, r *http.Request) {
	if !isOperator(r) {
		writeError(w, http.StatusForbidden, "operator or admin role required")
		return
	}
	f, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if !clearedFor(r, f) {
		writeError(w, http.StatusForbidden, "your clearance does not reach this connection's classification")
		return
	}
	if h.DeadLetters == nil {
		writeError(w, http.StatusNotImplemented, "dead-letter viewing is not enabled")
		return
	}
	limit := defaultLetters
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxLetters)
	}
	letters, scanned, partial, err := h.DeadLetters.Recent(r.Context(), f.ID, limit)
	if err != nil {
		h.logger.Warn("reading dead letters failed", zap.String("feed", f.ID), zap.Error(err))
		writeError(w, http.StatusBadGateway, "dead letters could not be read")
		return
	}
	withPayload := r.URL.Query().Get("payload") == "true"
	resp := deadLettersResponse{Letters: make([]letterDTO, 0, len(letters)), Scanned: scanned, Partial: partial}
	for _, l := range letters {
		resp.Letters = append(resp.Letters, toLetterDTO(l, withPayload))
	}
	writeJSON(w, http.StatusOK, resp)
}
