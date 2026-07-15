package handlers

import "github.com/go-chi/chi/v5"

// RegisterSelfGetRoute mounts GET /self onto r. The caller supplies the /v1
// prefix and whatever middleware this entry's manifest group carries
// (requireOIDCConfirmed, requireAuth) — deliberately NOT requireVerifiedEmail,
// so accounts with an unverified email can still reach this endpoint (the GUI
// renders the "verify your email" page from it).
func RegisterSelfGetRoute(r chi.Router, h *SelfHandler) {
	r.Get("/self", h.Get)
}

// RegisterSelfPutRoute mounts PUT /self onto r. The caller supplies the /v1
// prefix and this entry's manifest middleware group, which includes
// requireVerifiedEmail — only accounts with a verified email may update their
// own profile.
func RegisterSelfPutRoute(r chi.Router, h *SelfHandler) {
	r.Put("/self", h.Put)
}
