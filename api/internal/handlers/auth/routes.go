package auth

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// OIDCRoutes is what RegisterRoutes needs from the OIDC handler: the
// authorization-code start and callback of GET /oidc/{provider}/start and
// GET /oidc/{provider}/callback. The OIDC handler (task 006) provides them.
type OIDCRoutes interface {
	Start(w http.ResponseWriter, r *http.Request)
	Callback(w http.ResponseWriter, r *http.Request)
}

// RegisterRoutes mounts the /v1/auth routes onto r. The caller applies the
// group's middleware (requireOIDCConfirmed) first; RegisterRoutes adds none.
// oidc may be nil (an untyped nil) when OIDC is not wired, in which case the
// two OIDC routes are not mounted.
func RegisterRoutes(r chi.Router, h *Handler, oidc OIDCRoutes) {
	r.Post("/register", h.Register)
	r.Post("/login", h.Login)
	r.Post("/anonymous", h.Anonymous)
	r.Post("/email-code/request", h.EmailCodeRequest)
	r.Post("/email-code/verify", h.EmailCodeVerify)
	r.Post("/password-reset/request", h.PasswordResetRequest)
	r.Post("/password-reset/confirm", h.PasswordResetConfirm)
	r.Post("/token", h.Token)
	r.Post("/handoff", h.Handoff)
	r.Get("/providers", h.Providers)

	if oidc != nil {
		r.Get("/oidc/{provider}/start", oidc.Start)
		r.Get("/oidc/{provider}/callback", oidc.Callback)
	}
}
