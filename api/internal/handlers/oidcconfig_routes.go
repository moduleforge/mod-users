package handlers

import "github.com/go-chi/chi/v5"

// RegisterOIDCConfigRoutes mounts every route in the /v1/oidc-config group
// onto r. The caller is responsible for applying any conditional-mounting
// logic (e.g. only mounting when TOKEN_DISPLAY != none) and for applying
// any middleware before calling this function.
//
// Two handler args are required because the per-provider CRUD routes are
// served by a separate *ProvidersHandler value that is not reachable from
// *OIDCConfigHandler. Passing both avoids a structural change to either type.
//
// Routes registered (all paths relative to the prefix the caller supplies):
//
//	GET  /status
//	POST /confirm
//	GET  /saved
//	POST /providers
//	GET  /providers/{id}
//	PUT  /providers/{id}
//	DELETE /providers/{id}
//	GET  /setup-token
func RegisterOIDCConfigRoutes(r chi.Router, h *OIDCConfigHandler, p *ProvidersHandler) {
	r.Get("/status", h.Status)
	r.Post("/confirm", h.Confirm)
	r.Get("/saved", h.Saved)
	r.Post("/providers", p.Create)
	r.Get("/providers/{id}", p.Get)
	r.Put("/providers/{id}", p.Update)
	r.Delete("/providers/{id}", p.Revert)
	r.Get("/setup-token", h.SetupToken)
}
