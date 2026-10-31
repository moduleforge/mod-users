package usersservice

import (
	authzservice "github.com/moduleforge/authz-api/service"
)

// *CollaboratorGate must satisfy mod-authz's hook structurally, because
// mod-authz declares the interface and must not import mod-users. If the
// signature in either module changes, this stops compiling.
var _ authzservice.CollaboratorGate = (*CollaboratorGate)(nil)
