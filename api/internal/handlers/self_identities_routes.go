package handlers

import "github.com/go-chi/chi/v5"

// RegisterSelfIdentitiesReadRoute mounts GET /self/identities onto r. The
// caller supplies the /v1 prefix and whatever middleware this entry's
// manifest group carries (requireOIDCConfirmed, requireAuth) — deliberately
// NOT requireVerifiedEmail, so accounts with an unverified email can still
// list their own identities, matching the GET /self rationale in
// self_routes.go.
func RegisterSelfIdentitiesReadRoute(r chi.Router, h *IdentitiesHandler) {
	r.Get("/self/identities", h.List)
}

// RegisterSelfIdentitiesWriteRoutes mounts the six credential-mutating
// identity endpoints onto r. The caller supplies the /v1 prefix and this
// entry's manifest middleware group, which adds requireVerifiedEmail on top
// of the read group's middleware (requireOIDCConfirmed + requireAuth). The
// step-up request/verify endpoints belong in this verified-email-gated
// group, exactly as api/cmd/server/main.go mounts them today
// (main.go:539-549).
func RegisterSelfIdentitiesWriteRoutes(r chi.Router, h *IdentitiesHandler) {
	r.Post("/self/identities/oidc/{provider}/start", h.StartLink)
	r.Delete("/self/identities/{identity_uuid}", h.Unlink)
	r.Post("/self/credential/password", h.SetPassword)
	r.Delete("/self/credential/password", h.RemovePassword)
	r.Post("/self/credential/step-up", h.StepUpRequest)
	r.Post("/self/credential/step-up/verify", h.StepUpVerify)
}
