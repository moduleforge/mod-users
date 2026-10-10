package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/moduleforge/mod-users/api/internal/config"
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

	// CORS — default deny. See corsOptions for the policy.
	if opts, ok := corsOptions(cfg); ok {
		r.Use(cors.Handler(opts))
	}

	srv := &http.Server{
		Addr:    cfg.Server.Addr,
		Handler: r,
	}

	return srv, r
}

// corsOptions derives the CORS policy from cfg. ok is false when no CORS
// middleware should be installed at all, so cross-origin requests receive no
// CORS headers and browsers block them.
//
//   - Explicit allow-list (no "*"): the listed origins are echoed back and
//     credentials are allowed.
//   - Empty CORS_ORIGINS in DeployModeLocal: a documented dev convenience,
//     allow-all ("*") WITHOUT credentials.
//   - Empty CORS_ORIGINS in any other mode: deny (ok == false).
//   - "*" in the list outside DeployModeLocal: config.Load rejects this at
//     startup; if a hand-built Config reaches here anyway, fail closed (deny).
//     In local mode "*" is honored without credentials.
//
// The wildcard is never combined with credentials.
func corsOptions(cfg *config.Config) (cors.Options, bool) {
	origins := parseOrigins(cfg.Server.CORSOrigins)
	local := cfg.DeployMode == config.DeployModeLocal

	wildcard := len(origins) == 0 && local
	for _, o := range origins {
		if o == "*" {
			wildcard = true
		}
	}
	if len(origins) == 0 && !local {
		return cors.Options{}, false
	}
	if wildcard && !local {
		return cors.Options{}, false
	}
	if wildcard {
		origins = []string{"*"}
	}

	return cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-App", "X-Request-ID", "X-Step-Up-Token"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: !wildcard,
		MaxAge:           300,
	}, true
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
