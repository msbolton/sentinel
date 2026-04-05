package httputil

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

// Error writes a JSON error response.
func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, map[string]interface{}{
		"statusCode": status,
		"message":    message,
	})
}

// ErrorWithLogger writes a JSON error response and logs the error.
func ErrorWithLogger(w http.ResponseWriter, status int, message string, logger *zap.Logger, err error) {
	if err != nil {
		logger.Error(message, zap.Error(err))
	}
	Error(w, status, message)
}

// NoContent writes a 204 No Content response.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
