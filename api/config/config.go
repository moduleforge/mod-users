// Package config is the public facade for users-module configuration.
// It re-exports types and functions from the internal/config package so that
// modules outside users-module/api can reference them.
package config

import inner "github.com/moduleforge/mod-users/api/internal/config"

// Type aliases — interchangeable with the internal types (Go spec: same type).
type Config = inner.Config
type DeployMode = inner.DeployMode
type TokenDisplay = inner.TokenDisplay
type OnboardingConfig = inner.OnboardingConfig
type DBConfig = inner.DBConfig
type AuthConfig = inner.AuthConfig
type ServerConfig = inner.ServerConfig
type LocalAuthConfig = inner.LocalAuthConfig
type SMTPConfig = inner.SMTPConfig
type OTelConfig = inner.OTelConfig
type BootState = inner.BootState
type BootStateResult = inner.BootStateResult
type ProviderRegistry = inner.ProviderRegistry
type Provider = inner.Provider

// Re-export constants.
const (
	DeployModeLocal      = inner.DeployModeLocal
	DeployModeServerless = inner.DeployModeServerless
	DeployModeK8s        = inner.DeployModeK8s

	TokenDisplayBoth      = inner.TokenDisplayBoth
	TokenDisplayStderr    = inner.TokenDisplayStderr
	TokenDisplayLocalhost = inner.TokenDisplayLocalhost
	TokenDisplayNone      = inner.TokenDisplayNone

	BootStateConfirmedOK      = inner.BootStateConfirmedOK
	BootStateConfirmedOptOut  = inner.BootStateConfirmedOptOut
	BootStateInitFailed       = inner.BootStateInitFailed
	BootStateNoEnvNoFlag      = inner.BootStateNoEnvNoFlag
)

// Load reads configuration from environment variables, applies
// per-deployment-mode defaults, and validates required fields.
func Load() (*Config, error) {
	return inner.Load()
}
