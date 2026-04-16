// Package config loads service configuration from environment variables,
// applies deployment-mode defaults, and validates that all required fields
// are present. Consumers should call Load once at startup and treat the
// returned Config as read-only.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DeployMode represents the environment the service is running in.
type DeployMode string

const (
	DeployModeLocal      DeployMode = "local"
	DeployModeServerless DeployMode = "serverless"
	DeployModeK8s        DeployMode = "k8s"
)

// validDeployModes is the set of accepted DEPLOY_MODE values.
var validDeployModes = map[DeployMode]bool{
	DeployModeLocal:      true,
	DeployModeServerless: true,
	DeployModeK8s:        true,
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

// OIDCConfig holds settings for the pluggable OIDC provider.
type OIDCConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	// ClaimStyle selects the ClaimMapper implementation:
	// google | microsoft | keycloak | cognito | auth0 | authelia | generic
	ClaimStyle string
	AdminRole  string
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
	OIDC       OIDCConfig
	Server     ServerConfig
	LocalAuth  LocalAuthConfig
	SMTP       SMTPConfig
	OTel       OTelConfig
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
			fmt.Sprintf("DEPLOY_MODE: invalid value %q (must be local, serverless, or k8s)", rawMode))
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

	cfg := &Config{
		DeployMode: mode,
		DB: DBConfig{
			URL:             os.Getenv("DB_URL"),
			MaxConns:        maxConns,
			MaxConnLifetime: maxConnLifetime,
			MaxConnIdleTime: maxConnIdleTime,
		},
		OIDC: OIDCConfig{
			IssuerURL:    os.Getenv("OIDC_ISSUER_URL"),
			ClientID:     os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
			ClaimStyle:   os.Getenv("OIDC_CLAIM_STYLE"),
			AdminRole:    os.Getenv("OIDC_ADMIN_ROLE"),
		},
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
	}

	if err := validate(cfg, parseErrors); err != nil {
		return nil, err
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
		{"SMTP_HOST", cfg.SMTP.Host},
		{"SMTP_FROM", cfg.SMTP.From},
	}

	// OIDC fields are required in non-local modes. In local mode, the API
	// starts without OIDC (local auth only) if these are missing.
	if cfg.DeployMode != DeployModeLocal {
		required = append(required,
			check{"OIDC_ISSUER_URL", cfg.OIDC.IssuerURL},
			check{"OIDC_CLIENT_ID", cfg.OIDC.ClientID},
			check{"OIDC_CLIENT_SECRET", cfg.OIDC.ClientSecret},
			check{"OIDC_CLAIM_STYLE", cfg.OIDC.ClaimStyle},
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

	// SMTP_PORT is numeric so we check it separately.
	if cfg.SMTP.Port == 0 {
		problems = append(problems, "missing required environment variables: SMTP_PORT")
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
