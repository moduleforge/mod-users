package usersservice_test

import "github.com/moduleforge/mod-users/api/usersservice"

// Compile-only check: the facade must re-export the API key and profile
// summary types so consumers never need the internal package. If an alias is
// removed this file stops compiling.
var (
	_ usersservice.IssueAPIKeyInput
	_ usersservice.IssuedAPIKey
	_ usersservice.ProfileSummary
)
