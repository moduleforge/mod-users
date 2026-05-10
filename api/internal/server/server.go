package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/moduleforge/users-module/api/internal/config"
)

// New builds an http.Server with a chi router, base middleware, and CORS.
// Callers mount route groups on the returned Router before starting.
func New(cfg *config.Config) (*http.Server, *chi.Mux) {
	r := chi.NewRouter()

	// Base middleware stack (order matters).
	//
	// DO NOT insert chi/middleware.RealIP or any other X-Forwarded-For
	// rewriter in this base stack. The /v1/oidc-config/setup-token
	// endpoint relies on r.RemoteAddr being the peer's real address for
	// loopback-only access control; an XFF-aware middleware upstream of
	// that handler would let any external client spoof loopback by
	// setting a header. If per-route XFF trust is ever needed, scope it
	// to the specific route group that requires it — never the base.
	r.Use(RequestID)
	r.Use(Recoverer)
	r.Use(AccessLog)

	// CORS — allow configured origins or all in local mode.
	origins := parseOrigins(cfg.Server.CORSOrigins)
	if len(origins) == 0 && cfg.DeployMode == config.DeployModeLocal {
		origins = []string{"*"}
	}
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-App", "X-Request-ID", "X-Step-Up-Token"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	srv := &http.Server{
		Addr:    cfg.Server.Addr,
		Handler: r,
	}

	return srv, r
}

func parseOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}
