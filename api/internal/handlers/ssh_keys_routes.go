package handlers

import (
	"github.com/go-chi/chi/v5"
	coreAuthz "github.com/moduleforge/core-api/authz"
)

// RegisterSelfSSHKeysReadRoute mounts GET /self/ssh-keys onto r. The caller
// supplies the /v1 prefix and this entry's manifest middleware group
// (requireOIDCConfirmed, requireAuth) -- deliberately NOT
// requireVerifiedEmail, matching RegisterSelfIdentitiesReadRoute
// (self_identities_routes.go): listing does not require step-up either, so
// an account with an unverified email can still see its own registered
// keys. The registrar also enforces coreAuthz.RequireAuth itself, inside a
// chi Group, as defense in depth; the module's token-verifying requireAuth
// must still run first in production.
func RegisterSelfSSHKeysReadRoute(r chi.Router, h *SSHKeysHandler) {
	r.Group(func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth)
		r.Get("/self/ssh-keys", h.ListSelf)
	})
}

// RegisterSelfSSHKeysWriteRoutes mounts the two credential-mutating
// self-service SSH-key endpoints onto r. The caller supplies the /v1 prefix
// and this entry's manifest middleware group, which adds
// requireVerifiedEmail on top of the read group's middleware
// (requireOIDCConfirmed + requireAuth). Both endpoints additionally enforce
// step-up inside the handler when AUTH_REQUIRE_STEP_UP/h.stepUpRequired is
// on. The registrar also enforces coreAuthz.RequireAuth itself, inside a chi
// Group, as defense in depth; the module's token-verifying requireAuth must
// still run first in production.
func RegisterSelfSSHKeysWriteRoutes(r chi.Router, h *SSHKeysHandler) {
	r.Group(func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth)
		r.Post("/self/ssh-keys", h.RegisterSelf)
		r.Delete("/self/ssh-keys/{key_uuid}", h.RevokeSelf)
	})
}

// RegisterUserAccountSSHKeyRoutes mounts the operator SSH-key endpoints for
// a target account onto r: list, register, and revoke, at
// /user-accounts/{uuid}/ssh-keys[/{key_uuid}] (docs/architecture/
// ssh-keys.md D9). The caller supplies the /v1 prefix and this entry's
// manifest middleware group (requireOIDCConfirmed, requireAuth,
// requireVerifiedEmail) -- the same group RegisterAccountRoutes'
// /user-accounts entries use (account_routes.go). Register/revoke are
// step-up-gated inside the handler when the path {uuid} is the caller's
// own account (phase-1 security-001 fix); a genuine
// operator-on-behalf-of-a-different-account call is not gated. The registrar
// also enforces coreAuthz.RequireAuth itself, inside a chi Group, as defense
// in depth; the module's token-verifying requireAuth must still run first in
// production.
func RegisterUserAccountSSHKeyRoutes(r chi.Router, h *SSHKeysHandler) {
	r.Group(func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth)
		r.Get("/user-accounts/{uuid}/ssh-keys", h.ListForAccount)
		r.Post("/user-accounts/{uuid}/ssh-keys", h.RegisterForAccount)
		r.Delete("/user-accounts/{uuid}/ssh-keys/{key_uuid}", h.RevokeForAccount)
	})
}
