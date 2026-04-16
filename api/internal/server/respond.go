// Package server provides HTTP server construction and JSON response helpers.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("json encode failed", "error", err)
	}
}

// Error writes a structured error response.
func Error(w http.ResponseWriter, status int, code string, message string) {
	JSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

// ErrorWithDetails writes a structured error response with extra details.
func ErrorWithDetails(w http.ResponseWriter, status int, code string, message string, details any) {
	JSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}

// Decode reads JSON from the request body into v.
func Decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
