// Package server provides HTTP server construction and JSON response helpers.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
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
//
// This is a thin wrapper over apiresp's envelope construction
// (apiresp.Envelope/ErrorBody, written via apiresp.WriteJSON) rather than a
// parallel reimplementation, so the wire shape stays byte-for-byte
// consistent with apiresp.WriteError's output — including for the many
// literal server.Error(w, status, "code", msg) call sites left as-is here
// and migrated onto sentinel-driven apiresp.WriteError(w, r, err) calls in
// Phase 2 (see docs/mf-standards/architecture/api-response-design.md
// "Go-layer ownership" and this task's Requirement 5). Sentinel-classified
// errors should prefer apiresp.WriteError(w, r, err) directly; Error exists
// for call sites that already know their status/code/message explicitly.
func Error(w http.ResponseWriter, status int, code string, message string) {
	apiresp.WriteJSON(w, status, apiresp.Envelope{
		Error: apiresp.ErrorBody{Code: code, Message: message},
	})
}

// ErrorWithDetails writes a structured error response with field-level
// details, via the same apiresp envelope construction as Error.
func ErrorWithDetails(w http.ResponseWriter, status int, code string, message string, details []apiresp.FieldError) {
	apiresp.WriteJSON(w, status, apiresp.Envelope{
		Error: apiresp.ErrorBody{Code: code, Message: message, Details: details},
	})
}

// Decode reads JSON from the request body into v.
func Decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
