package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// RegisterSelfRoutes mounts GET/PUT /self onto r. The caller supplies the /v1
// prefix and the group-level middleware (requireOIDCConfirmed, requireAuth)
// BEFORE calling this function; RegisterSelfRoutes adds neither a prefix nor
// those gates itself.
//
// The email-verification gate is deliberately NOT applied at the group level.
// The caller passes requireVerifiedEmail in, and this function applies it to
// PUT /self only, via a nested r.Group — keeping GET /self reachable to accounts
// whose email is not yet verified (the GUI renders the "verify your email" page
// from it) while PUT /self stays restricted to verified accounts.
func RegisterSelfRoutes(r chi.Router, h *SelfHandler, requireVerifiedEmail func(http.Handler) http.Handler) {
	// GET /self bypasses the email-verification gate.
	r.Get("/self", h.Get)

	// PUT /self requires a verified email.
	r.Group(func(r chi.Router) {
		r.Use(requireVerifiedEmail)
		r.Put("/self", h.Put)
	})
}
