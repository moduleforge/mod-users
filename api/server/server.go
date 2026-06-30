// Package server is the public facade for users-module HTTP server construction.
// It re-exports the server constructor from internal/server.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/mod-users/api/config"
	inner "github.com/moduleforge/mod-users/api/internal/server"
)

// New constructs an *http.Server and a *chi.Mux from the provided configuration.
func New(cfg *config.Config) (*http.Server, *chi.Mux) {
	return inner.New(cfg)
}
