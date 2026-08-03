// Package config loads service configuration from environment variables,
// applies deployment-mode defaults, and validates that all required fields
// are present. Consumers should call Load once at startup and treat the
// returned Config as read-only.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// DeployMode represents the environment the service is running in.
type DeployMode string

const (
	// DeployModeLocal is a developer machine or CI checkout. It is the
	// only mode that relaxes defaults for convenience — notably the
	// allow-all CORS origin list in api/internal/server.
	DeployModeLocal DeployMode = "local"

	// DeployModeServerless is a scale-to-zero runtime (Cloud Run, Lambda
	// and friends) where many short-lived instances share one database,
	// so the connection pool defaults small.
	DeployModeServerless DeployMode = "serverless"

	// DeployModeK8s is an orchestrated container deployment.
	DeployModeK8s DeployMode = "k8s"

	// DeployModeContainerHost is an unorchestrated container deployment:
	// a single host running the stack under a container runtime (Docker,
	// Podman, plain containerd) with no orchestrator. It is the
	// counterpart to DeployModeK8s and carries no relaxed defaults — it
	// takes the non-serverless connection-pool default and does not get
	// DeployModeLocal's allow-all CORS origin list.
	DeployModeContainerHost DeployMode = "container-host"
)

// TokenDisplay controls how the OIDC onboarding setup token is
// surfaced when the API boots into an unconfirmed state. Values are
// parsed case-insensitively from the TOKEN_DISPLAY env var.
type TokenDisplay string

const (
	// TokenDisplayBoth emits the setup token both to stderr as a
	// human-scannable banner AND mounts the loopback-gated
	// /v1/oidc-config/setup-token endpoint. Default behavior; the most
	// discoverable option for a developer pulling the repo for the
	// first time.
	TokenDisplayBoth TokenDisplay = "both"

	// TokenDisplayStderr emits the banner only. Preferred for
	// deployments where the container logs are the canonical source of
	// truth (k8s with log aggregators, docker with a rotating log
	// driver). No /setup-token endpoint mounted.
	TokenDisplayStderr TokenDisplay = "stderr"

	// TokenDisplayLocalhost mounts the /setup-token endpoint only.
	// Useful when stderr is noisy or the operator prefers to retrieve
	// the token with `docker exec ... wget`.
	TokenDisplayLocalhost TokenDisplay = "localhost"

	// TokenDisplayNone is the production-strict escape hatch: reverts
	// to Phase 9.1 fail-fast. An unconfirmed boot state causes
	// main.go to log and exit non-zero, no onboarding endpoints are
	// mounted, and no setup token is generated. Use this in prod to
	// prevent silent entry into the recovery flow.
	TokenDisplayNone TokenDisplay = "none"
)

// validTokenDisplays is the set of accepted TOKEN_DISPLAY values.
var validTokenDisplays = map[TokenDisplay]bool{
	TokenDisplayBoth:      true,
	TokenDisplayStderr:    true,
	TokenDisplayLocalhost: true,
	TokenDisplayNone:      true,
}

// OnboardingConfig bundles the knobs that govern the phase 9.9a
// onboarding flow. Kept as its own nested struct so a future addition
// (e.g. token TTL) doesn't clutter the top-level Config.
type OnboardingConfig struct {
	// TokenDisplay chooses the surface for the one-shot setup token
	// emitted on unconfirmed boots. See the TokenDisplay consts for
	// semantics.
	TokenDisplay TokenDisplay
}

// validDeployModes is the set of accepted DEPLOY_MODE values. Load's
// rejection message in this same package spells the set out in prose; keep
// the two in sync when adding a mode.
var validDeployModes = map[DeployMode]bool{
	DeployModeLocal:         true,
	DeployModeServerless:    true,
	DeployModeK8s:           true,
	DeployModeContainerHost: true,
}

// DBConfig holds connection pool settings for the Postgres backend.
// The pool itself is wired in Phase 3 (Task 3.1); this struct just
// carries the configuration that will be passed to pgxpool.
type DBConfig struct {
	URL             string
	MaxConns        int
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// AuthConfig holds cross-cutting settings for the authentication subsystem.
// Per-provider settings live in the ProviderRegistry; AuthConfig carries the
// bits that apply regardless of which provider the user came in on.
type AuthConfig struct {
	// AdminRole is the role name (post-normalization, lowercased) that confers
	// admin privileges when present in a Principal.Roles slice. Defaults to
	// "admin" when the env var AUTH_ADMIN_ROLE is unset.
	AdminRole string

	// FrontendReturnURL is the absolute URL of the GUI page that completes the
	// OAuth callback handoff (e.g. "http://localhost:3000/auth/oidc/return").
	// Required when any provider is enabled.
	FrontendReturnURL string

	// OAuthRedirectBaseURL is the public base URL of this API service — the
	// callback URI registered with each OIDC provider is built as
	// "<OAuthRedirectBaseURL>/v1/auth/oidc/{provider}/callback".
	// Required when any provider is enabled.
	OAuthRedirectBaseURL string

	// RequireStepUpForCredentialChange gates every credential-mutating endpoint
	// (POST/DELETE password, DELETE OIDC identity, POST link-mode start) behind
	// a short-lived step-up token obtained via POST /v1/self/credential/step-up
	// + /verify. Default is false (off). Set AUTH_REQUIRE_STEP_UP=true|1|yes
	// to enable bank-style "prove you still own this email" enforcement.
	RequireStepUpForCredentialChange bool
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr            string
	ShutdownTimeout time.Duration
	CORSOrigins     string // comma-separated allowed origins
	GUIBaseURL      string // base URL of the GUI for links in emails
}

// LocalAuthConfig holds settings for the local (non-OIDC) auth subsystem.
type LocalAuthConfig struct {
	JWTSecret        string
	EmailCodeTTL     time.Duration
	PasswordResetTTL time.Duration
	LocalIssuer      string
}

// SMTPConfig holds outbound email settings.
type SMTPConfig struct {
	Host string
	Port int
	From string
	User string
	Pass string
}

// OTelConfig holds OpenTelemetry export settings.
type OTelConfig struct {
	ExporterEndpoint string
	ServiceName      string
}

// Config is the top-level configuration struct populated by Load.
type Config struct {
	DB         DBConfig
	Auth       AuthConfig
	Providers  ProviderRegistry
	Server     ServerConfig
	LocalAuth  LocalAuthConfig
	SMTP       SMTPConfig
	OTel       OTelConfig
	Onboarding OnboardingConfig
	DeployMode DeployMode
}

// Load reads configuration from environment variables, applies
// per-deployment-mode defaults, and validates that all required fields
// are present. On validation failure it returns a single error that
// lists every problem so the operator can fix them all at once.
func Load() (*Config, error) {
	// parseErrors accumulates non-fatal parse problems so we can report
	// them alongside missing-field errors in one shot.
	var parseErrors []string

	rawMode := getEnv("DEPLOY_MODE", string(DeployModeLocal))
	mode := DeployMode(rawMode)
	if !validDeployModes[mode] {
		parseErrors = append(parseErrors,
			fmt.Sprintf("DEPLOY_MODE: invalid value %q (must be local, serverless, k8s, or container-host)", rawMode))
		// Fall back to local so the rest of Load can proceed.
		mode = DeployModeLocal
	}

	maxConns := 20
	if mode == DeployModeServerless {
		maxConns = 4
	}
	// Allow explicit override regardless of mode.
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			parseErrors = append(parseErrors,
				fmt.Sprintf("DB_MAX_CONNS: invalid integer %q", v))
		} else {
			maxConns = n
		}
	}

	smtpPort := 0
	if v := os.Getenv("SMTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			parseErrors = append(parseErrors,
				fmt.Sprintf("SMTP_PORT: invalid integer %q", v))
		} else {
			smtpPort = n
		}
	}

	maxConnLifetime, lifetimeErr := parseDuration("DB_MAX_CONN_LIFETIME", 5*time.Minute)
	if lifetimeErr != nil {
		parseErrors = append(parseErrors, lifetimeErr.Error())
	}

	maxConnIdleTime, idleErr := parseDuration("DB_MAX_CONN_IDLE_TIME", 1*time.Minute)
	if idleErr != nil {
		parseErrors = append(parseErrors, idleErr.Error())
	}

	shutdownTimeout, shutdownErr := parseDuration("SERVER_SHUTDOWN_TIMEOUT", 25*time.Second)
	if shutdownErr != nil {
		parseErrors = append(parseErrors, shutdownErr.Error())
	}

	emailCodeTTL, emailCodeErr := parseDuration("EMAIL_CODE_TTL", 5*time.Minute)
	if emailCodeErr != nil {
		parseErrors = append(parseErrors, emailCodeErr.Error())
	}

	passwordResetTTL, passwordResetErr := parseDuration("PASSWORD_RESET_TTL", 30*time.Minute)
	if passwordResetErr != nil {
		parseErrors = append(parseErrors, passwordResetErr.Error())
	}

	registry, registryErr := LoadProviders()
	if registryErr != nil {
		parseErrors = append(parseErrors, registryErr.Error())
		// Keep registry non-nil so downstream validation is well-defined.
		registry = ProviderRegistry{}
	}

	tokenDisplay := TokenDisplay(strings.ToLower(strings.TrimSpace(getEnv("TOKEN_DISPLAY", string(TokenDisplayBoth)))))
	if !validTokenDisplays[tokenDisplay] {
		parseErrors = append(parseErrors,
			fmt.Sprintf("TOKEN_DISPLAY: invalid value %q (must be both, stderr, localhost, or none)", string(tokenDisplay)))
		// Fall back to the safe "both" default so the rest of Load can
		// proceed and the operator sees the full list of problems.
		tokenDisplay = TokenDisplayBoth
	}

	cfg := &Config{
		DeployMode: mode,
		DB: DBConfig{
			URL:             os.Getenv("DB_URL"),
			MaxConns:        maxConns,
			MaxConnLifetime: maxConnLifetime,
			MaxConnIdleTime: maxConnIdleTime,
		},
		Auth: AuthConfig{
			AdminRole:                        getEnv("AUTH_ADMIN_ROLE", "admin"),
			FrontendReturnURL:                os.Getenv("AUTH_FRONTEND_RETURN_URL"),
			OAuthRedirectBaseURL:             os.Getenv("AUTH_OAUTH_REDIRECT_BASE_URL"),
			RequireStepUpForCredentialChange: parseBoolEnv(os.Getenv("AUTH_REQUIRE_STEP_UP")),
		},
		Providers: registry,
		Server: ServerConfig{
			Addr:            getEnv("SERVER_ADDR", ":8080"),
			ShutdownTimeout: shutdownTimeout,
			CORSOrigins:     os.Getenv("CORS_ORIGINS"),
			GUIBaseURL:      getEnv("GUI_BASE_URL", "http://localhost:3000"),
		},
		LocalAuth: LocalAuthConfig{
			JWTSecret:        os.Getenv("JWT_SECRET"),
			EmailCodeTTL:     emailCodeTTL,
			PasswordResetTTL: passwordResetTTL,
			LocalIssuer:      getEnv("LOCAL_ISSUER", "users-module-local"),
		},
		SMTP: SMTPConfig{
			Host: os.Getenv("SMTP_HOST"),
			Port: smtpPort,
			From: os.Getenv("SMTP_FROM"),
			User: os.Getenv("SMTP_USER"),
			Pass: os.Getenv("SMTP_PASS"),
		},
		OTel: OTelConfig{
			ExporterEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
			ServiceName:      getEnv("OTEL_SERVICE_NAME", "users-api"),
		},
		Onboarding: OnboardingConfig{
			TokenDisplay: tokenDisplay,
		},
	}

	if err := validate(cfg, parseErrors); err != nil {
		return nil, err
	}

	// SMTP is optional: deployments that never need to send email (e.g. a
	// personal single-owner instance) can boot without it configured at
	// all. Surface a non-fatal warning so an operator who *meant* to
	// configure it notices the gap without the boot failing.
	if cfg.SMTP.Host == "" {
		slog.Warn("SMTP not configured; email verification and password-reset emails will not be sent")
	}

	return cfg, nil
}

// validate checks that all required fields are non-zero and returns a single
// error listing every problem — missing fields and parse errors — so operators
// can fix them all in one restart rather than discovering them one by one.
func validate(cfg *Config, parseErrors []string) error {
	type check struct {
		field string
		value string
	}

	required := []check{
		{"DB_URL", cfg.DB.URL},
		{"JWT_SECRET", cfg.LocalAuth.JWTSecret},
	}

	// When any OIDC provider is enabled, the OAuth callback plumbing needs
	// both a redirect base (for the URI registered with the provider) and a
	// frontend return URL (where the callback sends the browser after minting
	// a local JWT). With zero providers, neither is needed — local auth only.
	if len(cfg.Providers) > 0 {
		required = append(required,
			check{"AUTH_FRONTEND_RETURN_URL", cfg.Auth.FrontendReturnURL},
			check{"AUTH_OAUTH_REDIRECT_BASE_URL", cfg.Auth.OAuthRedirectBaseURL},
		)
	}

	var problems []string

	// Collect any parse errors accumulated during Load.
	problems = append(problems, parseErrors...)

	var missing []string
	for _, c := range required {
		if strings.TrimSpace(c.value) == "" {
			missing = append(missing, c.field)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, "missing required environment variables: "+strings.Join(missing, ", "))
	}

	// SMTP is never required on its own: a deployment that never sends
	// email (e.g. a personal single-owner instance) can leave it entirely
	// unset. But setting SMTP_HOST signals intent to use SMTP, so from
	// that point the rest of the fields (SMTP_PORT is numeric, so it is
	// checked separately from the string-valued `required` slice above)
	// must be present too — a half-configured SMTP block is a real
	// misconfiguration, not an intentional "off" state.
	if cfg.SMTP.Host != "" {
		var smtpMissing []string
		if cfg.SMTP.Port == 0 {
			smtpMissing = append(smtpMissing, "SMTP_PORT")
		}
		if strings.TrimSpace(cfg.SMTP.From) == "" {
			smtpMissing = append(smtpMissing, "SMTP_FROM")
		}
		if len(smtpMissing) > 0 {
			problems = append(problems,
				"SMTP_HOST is set but SMTP is only partially configured; missing: "+strings.Join(smtpMissing, ", "))
		}
	}

	if len(problems) == 0 {
		return nil
	}

	return errors.New("config: " + strings.Join(problems, "; "))
}

// getEnv returns the environment variable named by key, or fallback if
// the variable is unset or empty.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseBoolEnv accepts "1", "true", "yes" (case-insensitive) as truthy.
// Anything else — including empty — is false.
func parseBoolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// parseDuration reads key from the environment and parses it as a
// time.Duration. If the variable is absent, fallback is returned with a nil
// error. If the variable is set but unparseable, fallback is returned along
// with a descriptive error so Load can surface it to the operator.
func parseDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback, fmt.Errorf("%s: invalid duration %q", key, v)
	}
	return d, nil
}
