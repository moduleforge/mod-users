package handlers

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/users-module/api/internal/server"
)

// Live is the liveness probe. Always returns 200 — no DB check.
func Live(w http.ResponseWriter, r *http.Request) {
	server.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Ready is the readiness probe. Returns 200 if the DB is reachable, 503 otherwise.
func Ready(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			server.JSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
				"db":     err.Error(),
			})
			return
		}
		server.JSON(w, http.StatusOK, map[string]string{
			"status": "ready",
			"db":     "ok",
		})
	}
}
