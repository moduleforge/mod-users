package handlers

import "github.com/go-chi/chi/v5"

// RegisterSelfSSHKeysReadRoute mounts GET /self/ssh-keys onto r. The caller
// supplies the /v1 prefix and this entry's manifest middleware group
// (requireOIDCConfirmed, requireAuth) -- deliberately NOT
// requireVerifiedEmail, matching RegisterSelfIdentitiesReadRoute
// (self_identities_routes.go): listing does not require step-up either
// (design note D8/D9), so an account with an unverified email can still see
// its own registered keys.
func RegisterSelfSSHKeysReadRoute(r chi.Router, h *SSHKeysHandler) {
	r.Get("/self/ssh-keys", h.ListSelf)
}

// RegisterSelfSSHKeysWriteRoutes mounts the two credential-mutating
// self-service SSH-key endpoints onto r. The caller supplies the /v1 prefix
// and this entry's manifest middleware group, which adds
// requireVerifiedEmail on top of the read group's middleware
// (requireOIDCConfirmed + requireAuth). Both endpoints additionally enforce
// step-up inside the handler (design note D8) when
// AUTH_REQUIRE_STEP_UP/h.stepUpRequired is on.
func RegisterSelfSSHKeysWriteRoutes(r chi.Router, h *SSHKeysHandler) {
	r.Post("/self/ssh-keys", h.RegisterSelf)
	r.Delete("/self/ssh-keys/{key_uuid}", h.RevokeSelf)
}

// RegisterUserAccountSSHKeyRoutes mounts the operator SSH-key endpoints for
// a target account onto r: list, register, and revoke, at
// /user-accounts/{uuid}/ssh-keys[/{key_uuid}] (design note D9). The caller
// supplies the /v1 prefix and this entry's manifest middleware group
// (requireOIDCConfirmed, requireAuth, requireVerifiedEmail) -- the same
// group RegisterAccountRoutes' /user-accounts entries use
// (account_routes.go). Not step-up-gated: operator routes never require
// step-up (design note D8).
func RegisterUserAccountSSHKeyRoutes(r chi.Router, h *SSHKeysHandler) {
	r.Get("/user-accounts/{uuid}/ssh-keys", h.ListForAccount)
	r.Post("/user-accounts/{uuid}/ssh-keys", h.RegisterForAccount)
	r.Delete("/user-accounts/{uuid}/ssh-keys/{key_uuid}", h.RevokeForAccount)
}
