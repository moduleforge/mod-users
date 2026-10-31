// Package config loads service configuration from environment variables,
// applies deployment-mode defaults, and validates that all required fields
// are present. Consumers should call Load once at startup and treat the
// returned Config as read-only.
package config

import (
	"context"
	"crypto/ed25519"
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
	// credential-less allow-all CORS origin list in api/internal/server.
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
	// URL is the resolved Postgres connection string, per resolveDBURL:
	// MFAPP_DATABASE_URL when set, else DB_URL. Both names are supported
	// permanently — this is not a rename-in-progress.
	URL             string
	MaxConns        int
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// resolveDBURL returns MFAPP_DATABASE_URL when set, else DB_URL. Both
// names are supported permanently: MFAPP_DATABASE_URL is the name the
// app-mfmanager catalog-deploy engine injects into the apps it manages;
// DB_URL remains the name every self-hosted or non-catalog deployment
// sets directly (including app-mfmanager's own bootstrap connection —
// see plan/overview.md's "Key research findings"). This is not a
// rename-in-progress: neither name is scheduled for removal.
func resolveDBURL() string {
	if v := os.Getenv("MFAPP_DATABASE_URL"); v != "" {
		return v
	}
	if v := os.Getenv("DB_URL"); v != "" {
		slog.Debug("config: using DB_URL (MFAPP_DATABASE_URL not set)")
		return v
	}
	return ""
}

// Defaults and bounds for the access-token and session lifetimes (API surface
// section 8). MaxAccessTokenTTL is a hard ceiling: an access token is only a
// carrier for a server-side session that is re-checked on every request, so a
// longer-lived token buys nothing and widens the replay window.
const (
	DefaultAccessTokenTTL        = time.Hour
	MaxAccessTokenTTL            = time.Hour
	DefaultSessionMaxAge         = 720 * time.Hour
	DefaultSessionIdleTimeout    = 168 * time.Hour
	DefaultClientCredentialsTTL  = 15 * time.Minute
	DefaultImpersonationTTL      = 30 * time.Minute
	DefaultImpersonationMax      = 60 * time.Minute
	DefaultSelfServiceMaxAuthAge = 10 * time.Minute
	// DefaultAudience is the "aud" an access token carries when its session
	// has no app (AUTH_DEFAULT_AUDIENCE unset).
	DefaultAudience = "mod-users"
)

// AuthConfig holds cross-cutting settings for the authentication subsystem.
// Per-provider settings live in the ProviderRegistry; AuthConfig carries the
// bits that apply regardless of which provider the user came in on.
type AuthConfig struct {
	// FrontendReturnURL is the absolute URL of the GUI page that completes the
	// OAuth callback handoff (e.g. "http://localhost:3000/auth/oidc/return").
	// Required when any provider is enabled.
	FrontendReturnURL string

	// OAuthRedirectBaseURL is the public base URL of this API service — the
	// callback URI registered with each OIDC provider is built as
	// "<OAuthRedirectBaseURL>/v1/auth/oidc/{provider}/callback".
	// Required when any provider is enabled.
	OAuthRedirectBaseURL string

	// CookiePath is the public base path (e.g. "/manage") under which this
	// API is reachable when a proxy strips the mount prefix. When set, the
	// oidc_state cookie Path is CookiePath + "/v1/auth/oidc/" instead of being
	// derived from the request URL. Empty (the default) keeps the
	// request-derived behaviour. Env var USERS_COOKIE_PATH; validated by
	// ValidateCookieBasePath at load time. The oidc_state cookie is the only
	// cookie this module sets; no route reads an authentication cookie.
	CookiePath string

	// AccessTokenTTL is the lifetime of an interactive access token
	// (AUTH_ACCESS_TOKEN_TTL, default 1h, at most 1h). A token's exp is
	// min(iat + AccessTokenTTL, sessions.expires_at).
	AccessTokenTTL time.Duration

	// SessionMaxAge is the absolute lifetime of a session
	// (AUTH_SESSION_MAX_AGE, default 720h).
	SessionMaxAge time.Duration

	// SessionIdleTimeout is the refresh-lineage expiry: a session whose
	// refresh token is not used for this long can no longer be refreshed
	// (AUTH_SESSION_IDLE_TIMEOUT, default 168h).
	SessionIdleTimeout time.Duration

	// ClientCredentialsTTL is the lifetime of a client-credentials access
	// token (AUTH_CLIENT_CREDENTIALS_TTL, default 15m).
	ClientCredentialsTTL time.Duration

	// ImpersonationTTL is the lifetime of one impersonation token and of each
	// renewal (AUTH_IMPERSONATION_TTL, default 30m).
	ImpersonationTTL time.Duration

	// ImpersonationMax bounds an impersonation session's total lifetime from
	// its start, renewals included (AUTH_IMPERSONATION_MAX, default 60m; at
	// least ImpersonationTTL).
	ImpersonationMax time.Duration

	// SelfServiceMaxAuthAge is how recent a session's auth_time must be for
	// self-service credential and identifier changes
	// (AUTH_SELF_SERVICE_MAX_AUTH_AGE, default 10m).
	SelfServiceMaxAuthAge time.Duration

	// DefaultAudience is the "aud" of an access token whose session has no
	// app, and of client-credentials tokens that request none
	// (AUTH_DEFAULT_AUDIENCE, default "mod-users").
	DefaultAudience string

	// AcceptedAudiences is the explicit set of "aud" values bearer tokens may
	// carry (AUTH_ACCEPTED_AUDIENCES, comma-separated). Empty means the
	// default audience plus the slug of any registered app.
	AcceptedAudiences []string
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr            string
	ShutdownTimeout time.Duration
	CORSOrigins     string // comma-separated allowed origins
	GUIBaseURL      string // base URL of the GUI for links in emails
}

// LocalAuthConfig holds settings for the local (non-OIDC) auth subsystem.
// Access tokens are EdDSA (Ed25519) JWTs only.
type LocalAuthConfig struct {
	// JWTSecret is the optional explicit HMAC key for the OAuth state signer
	// (JWT_SECRET). It signs nothing else. An empty value means the OAuth
	// state key is derived from JWTSigningKey (see
	// internal/auth/derived_keys.go); an explicit value wins over derivation.
	JWTSecret string
	// JWTSigningKey is the Ed25519 private key that signs access tokens:
	// parsed from JWT_SIGNING_KEY (PKCS#8 PEM) when set, otherwise the key
	// persisted in mod_users.auth_signing_keys (bootstrapped on first boot;
	// see signing_key_bootstrap.go). Always non-nil after a successful Load.
	// The raw env value is never retained, and the key is never logged.
	JWTSigningKey ed25519.PrivateKey
	// JWTSigningKID is the optional JWT_SIGNING_KID, set as the "kid" header
	// on locally-issued JWTs when non-empty.
	JWTSigningKID string
	// JWTVerifyPublicKeys holds the Ed25519 public keys from
	// JWT_VERIFY_PUBLIC_KEYS (kid -> key; the empty kid is used for tokens
	// without a "kid" header). Nil when unset. Used for verify-only services
	// and for keeping retired signing keys valid during rotation.
	JWTVerifyPublicKeys map[string]ed25519.PublicKey
	EmailCodeTTL        time.Duration
	PasswordResetTTL    time.Duration
	LocalIssuer         string
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
//
// The Postgres connection string is resolved via resolveDBURL:
// MFAPP_DATABASE_URL is preferred when set, with DB_URL as a permanent
// fallback — both names remain supported indefinitely, not as a
// deprecation window.
//
// Access tokens are EdDSA only. JWT_SIGNING_KEY (PKCS#8 PEM Ed25519 private
// key) is optional: when it is unset and the resolved DB URL is present, Load
// fetches the key persisted in mod_users.auth_signing_keys, or generates and
// durably persists one on first boot (bootstrapSigningKeyFromDB /
// fetchOrGeneratePersistedSigningKey in signing_key_bootstrap.go). A
// persisted key that fails to parse fails loudly rather than being silently
// regenerated. JWT_SIGNING_KID (optional "kid" header) and
// JWT_VERIFY_PUBLIC_KEYS (PEM public key or JSON kid-to-PEM map) are
// validated too (see jwt_signing.go). JWT_SECRET is optional and only keys
// the OAuth state signer; when unset that key is derived from the signing
// key (internal/auth/derived_keys.go).
//
// The access-token and session lifetimes (AUTH_ACCESS_TOKEN_TTL,
// AUTH_SESSION_MAX_AGE, AUTH_SESSION_IDLE_TIMEOUT,
// AUTH_CLIENT_CREDENTIALS_TTL, AUTH_IMPERSONATION_TTL, AUTH_IMPERSONATION_MAX,
// AUTH_SELF_SERVICE_MAX_AUTH_AGE) must be positive Go durations;
// AUTH_ACCESS_TOKEN_TTL is capped at MaxAccessTokenTTL and
// AUTH_IMPERSONATION_MAX may not be shorter than AUTH_IMPERSONATION_TTL.
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

	jwtSigningKey, jwtSigningKID, jwtSigningProblems := loadJWTSigning()
	parseErrors = append(parseErrors, jwtSigningProblems...)
	jwtVerifyKeys, jwtVerifyProblems := loadJWTVerifyPublicKeys()
	parseErrors = append(parseErrors, jwtVerifyProblems...)

	authLifetimes, lifetimeProblems := loadAuthLifetimes()
	parseErrors = append(parseErrors, lifetimeProblems...)

	cookiePath := os.Getenv("USERS_COOKIE_PATH")
	if err := ValidateCookieBasePath(cookiePath); err != nil {
		parseErrors = append(parseErrors, "USERS_COOKIE_PATH: "+err.Error())
		cookiePath = ""
	}

	cfg := &Config{
		DeployMode: mode,
		DB: DBConfig{
			URL:             resolveDBURL(),
			MaxConns:        maxConns,
			MaxConnLifetime: maxConnLifetime,
			MaxConnIdleTime: maxConnIdleTime,
		},
		Auth: AuthConfig{
			FrontendReturnURL:     os.Getenv("AUTH_FRONTEND_RETURN_URL"),
			OAuthRedirectBaseURL:  os.Getenv("AUTH_OAUTH_REDIRECT_BASE_URL"),
			CookiePath:            cookiePath,
			AccessTokenTTL:        authLifetimes.AccessTokenTTL,
			SessionMaxAge:         authLifetimes.SessionMaxAge,
			SessionIdleTimeout:    authLifetimes.SessionIdleTimeout,
			ClientCredentialsTTL:  authLifetimes.ClientCredentialsTTL,
			ImpersonationTTL:      authLifetimes.ImpersonationTTL,
			ImpersonationMax:      authLifetimes.ImpersonationMax,
			SelfServiceMaxAuthAge: authLifetimes.SelfServiceMaxAuthAge,
			DefaultAudience:       strings.TrimSpace(getEnv("AUTH_DEFAULT_AUDIENCE", DefaultAudience)),
			AcceptedAudiences:     parseList(os.Getenv("AUTH_ACCEPTED_AUDIENCES")),
		},
		Providers: registry,
		Server: ServerConfig{
			Addr:            getEnv("SERVER_ADDR", ":8080"),
			ShutdownTimeout: shutdownTimeout,
			CORSOrigins:     os.Getenv("CORS_ORIGINS"),
			GUIBaseURL:      getEnv("GUI_BASE_URL", "http://localhost:3000"),
		},
		LocalAuth: LocalAuthConfig{
			JWTSecret:     os.Getenv("JWT_SECRET"),
			JWTSigningKey: jwtSigningKey,
			JWTSigningKID: jwtSigningKID,

			JWTVerifyPublicKeys: jwtVerifyKeys,
			EmailCodeTTL:        emailCodeTTL,
			PasswordResetTTL:    passwordResetTTL,
			LocalIssuer:         getEnv("LOCAL_ISSUER", "users-module-local"),
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

	// A blank or whitespace-only JWT_SECRET means "derive the OAuth state
	// key": normalize it to "" so LocalAuth.JWTSecret consistently signals
	// that.
	if strings.TrimSpace(cfg.LocalAuth.JWTSecret) == "" {
		cfg.LocalAuth.JWTSecret = ""
	}

	// Without JWT_SIGNING_KEY, fetch (or generate and persist) the signing
	// key in Postgres. Skipped when JWT_SIGNING_KEY was set but invalid (its
	// problem is already reported) and when no DB URL is configured (validate
	// reports the missing DB URL).
	if cfg.LocalAuth.JWTSigningKey == nil && cfg.DB.URL != "" && !jwtSigningKeyProblem(jwtSigningProblems) {
		key, err := bootstrapSigningKeyFromDB(context.Background(), cfg.DB.URL)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Sprintf("JWT_SIGNING_KEY: bootstrap from database failed: %v", err))
		} else {
			cfg.LocalAuth.JWTSigningKey = key
		}
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
		{"MFAPP_DATABASE_URL / DB_URL", cfg.DB.URL},
		{"AUTH_DEFAULT_AUDIENCE", cfg.Auth.DefaultAudience},
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

	// The signing key is always required. When it is absent and no
	// JWT_SIGNING_KEY problem explains why (invalid value, failed bootstrap),
	// report it, so Load can never return a Config without one.
	if cfg.LocalAuth.JWTSigningKey == nil && !jwtSigningKeyProblem(parseErrors) {
		problems = append(problems, "JWT_SIGNING_KEY: no signing key (set JWT_SIGNING_KEY or configure the database so one can be bootstrapped)")
	}

	var missing []string
	for _, c := range required {
		if strings.TrimSpace(c.value) == "" {
			missing = append(missing, c.field)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, "missing required environment variables: "+strings.Join(missing, ", "))
	}

	// CORS_ORIGINS may not contain any "*" outside DeployModeLocal: go-chi/cors
	// treats every allowed-origin entry containing "*" as a prefix/suffix
	// wildcard pattern (e.g. "https://*", "http*"), so rejecting only the
	// exact token "*" would let such patterns be served with credentials.
	// Every mode except local is production-like (serverless, k8s,
	// container-host). The error names only the offending entry's shape.
	if cfg.DeployMode != DeployModeLocal {
		for _, o := range strings.Split(cfg.Server.CORSOrigins, ",") {
			if strings.Contains(o, "*") {
				problems = append(problems,
					"CORS_ORIGINS must not contain wildcard (\"*\") entries when DEPLOY_MODE is "+string(cfg.DeployMode)+
						"; list exact origins (wildcards are only allowed in local mode, without credentials)")
				break
			}
		}
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

// jwtSigningKeyProblem reports whether problems (from loadJWTSigning) include
// one about JWT_SIGNING_KEY itself, in which case the operator supplied a key
// that failed to parse and Load must not silently fall back to the persisted
// one.
func jwtSigningKeyProblem(problems []string) bool {
	for _, p := range problems {
		if strings.HasPrefix(p, "JWT_SIGNING_KEY:") {
			return true
		}
	}
	return false
}

// parseList splits a comma-separated value, trimming whitespace and dropping
// empty entries. It returns nil for an empty or all-blank value.
func parseList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// authLifetimes groups the AUTH_* duration settings loadAuthLifetimes reads.
type authLifetimes struct {
	AccessTokenTTL        time.Duration
	SessionMaxAge         time.Duration
	SessionIdleTimeout    time.Duration
	ClientCredentialsTTL  time.Duration
	ImpersonationTTL      time.Duration
	ImpersonationMax      time.Duration
	SelfServiceMaxAuthAge time.Duration
}

// loadAuthLifetimes reads the access-token and session lifetime settings,
// applying the defaults of API surface section 8. Every value must be a
// positive duration; AUTH_ACCESS_TOKEN_TTL may not exceed MaxAccessTokenTTL
// and AUTH_IMPERSONATION_MAX may not be shorter than AUTH_IMPERSONATION_TTL.
// A rejected value falls back to its default and is reported in problems.
func loadAuthLifetimes() (authLifetimes, []string) {
	var problems []string
	read := func(key string, fallback time.Duration) time.Duration {
		d, err := parseDuration(key, fallback)
		if err != nil {
			problems = append(problems, err.Error())
			return fallback
		}
		if d <= 0 {
			problems = append(problems, key+": must be a positive duration")
			return fallback
		}
		return d
	}

	l := authLifetimes{
		AccessTokenTTL:        read("AUTH_ACCESS_TOKEN_TTL", DefaultAccessTokenTTL),
		SessionMaxAge:         read("AUTH_SESSION_MAX_AGE", DefaultSessionMaxAge),
		SessionIdleTimeout:    read("AUTH_SESSION_IDLE_TIMEOUT", DefaultSessionIdleTimeout),
		ClientCredentialsTTL:  read("AUTH_CLIENT_CREDENTIALS_TTL", DefaultClientCredentialsTTL),
		ImpersonationTTL:      read("AUTH_IMPERSONATION_TTL", DefaultImpersonationTTL),
		ImpersonationMax:      read("AUTH_IMPERSONATION_MAX", DefaultImpersonationMax),
		SelfServiceMaxAuthAge: read("AUTH_SELF_SERVICE_MAX_AUTH_AGE", DefaultSelfServiceMaxAuthAge),
	}
	if l.AccessTokenTTL > MaxAccessTokenTTL {
		problems = append(problems, fmt.Sprintf("AUTH_ACCESS_TOKEN_TTL: must not exceed %s", MaxAccessTokenTTL))
		l.AccessTokenTTL = MaxAccessTokenTTL
	}
	if l.ImpersonationMax < l.ImpersonationTTL {
		problems = append(problems, "AUTH_IMPERSONATION_MAX: must not be shorter than AUTH_IMPERSONATION_TTL")
	}
	return l, problems
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
