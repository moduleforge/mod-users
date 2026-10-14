package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/moduleforge/mod-users/api/internal/server"
)

// Live is the liveness probe. Always returns 200 — no DB check.
func Live(w http.ResponseWriter, r *http.Request) {
	server.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Pinger is satisfied by *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Ready is the readiness probe. Returns 200 if the DB is reachable, 503 otherwise.
// The 503 body carries a fixed value; error detail is logged only.
func Ready(pool Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			slog.ErrorContext(r.Context(), "readyz: database ping failed", "error", err)
			server.JSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
				"db":     "unavailable",
			})
			return
		}
		server.JSON(w, http.StatusOK, map[string]string{
			"status": "ready",
			"db":     "ok",
		})
	}
}
