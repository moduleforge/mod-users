package handlers

import (
	"github.com/go-chi/chi/v5"
	coreAuthz "github.com/moduleforge/core-api/authz"
)

// RegisterSelfGetRoute mounts GET /self onto r. The caller supplies the /v1
// prefix and whatever middleware this entry's manifest group carries
// (requireOIDCConfirmed, requireAuth) — deliberately NOT requireVerifiedEmail,
// so accounts with an unverified email can still reach this endpoint (the GUI
// renders the "verify your email" page from it). The registrar also enforces
// coreAuthz.RequireAuth itself, inside a chi Group, as defense in depth; the
// module's token-verifying requireAuth must still run first in production.
func RegisterSelfGetRoute(r chi.Router, h *SelfHandler) {
	r.Group(func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth)
		r.Get("/self", h.Get)
	})
}

// RegisterSelfPutRoute mounts PUT /self onto r. The caller supplies the /v1
// prefix and this entry's manifest middleware group, which includes
// requireVerifiedEmail — only accounts with a verified email may update their
// own profile. The registrar also enforces coreAuthz.RequireAuth itself,
// inside a chi Group, as defense in depth; the module's token-verifying
// requireAuth must still run first in production.
func RegisterSelfPutRoute(r chi.Router, h *SelfHandler) {
	r.Group(func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth)
		r.Put("/self", h.Put)
	})
}
