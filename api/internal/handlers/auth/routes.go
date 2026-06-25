package auth

import "github.com/go-chi/chi/v5"

// RegisterRoutes mounts every route in the /v1/auth group onto r. The caller
// is responsible for applying any middleware (e.g. requireOIDCConfirmed) before
// calling this function; RegisterRoutes does not add middleware itself.
//
// Signature chosen: two handler args are required because the OIDC start/
// callback/providers routes are served by a separate *OIDCHandler value that is
// not reachable from *Handler. Passing both avoids a structural change to either
// type.
func RegisterRoutes(r chi.Router, h *Handler, oidc *OIDCHandler) {
	r.Post("/register", h.Register)
	r.Post("/login", h.Login)
	r.Post("/anonymous", h.Anonymous)
	r.Post("/email-code/request", h.EmailCodeRequest)
	r.Post("/email-code/verify", h.EmailCodeVerify)
	r.Post("/password-reset/request", h.PasswordResetRequest)
	r.Post("/password-reset/confirm", h.PasswordResetConfirm)

	// OIDC provider discovery + authorization-code flow (unauthenticated).
	r.Get("/providers", oidc.ListProviders)
	r.Get("/oidc/{provider}/start", oidc.Start)
	r.Get("/oidc/{provider}/callback", oidc.Callback)
}
