// Package handlers — onboarding_boot.go extracts the setup-token
// mint-and-print branch of mod-users' own reference server's boot
// sequence (api/cmd/server/main.go) into a reusable, exported form that
// a generated app's startup hook can call.
package handlers

import (
	"context"
	"fmt"
	"log/slog"

	innerauth "github.com/moduleforge/mod-users/api/internal/auth"

	"github.com/moduleforge/mod-users/api/config"
)

// EnsureSetupTokenAndPrint mints (idempotently) a setup token when
// onboarding is not yet confirmed and the app's TokenDisplay isn't
// "none", printing it via the configured channel (stderr banner or a
// structured log line pointing at the loopback-only /setup-token
// endpoint). When TokenDisplay is "none", it instead requires
// onboarding to already be confirmed, returning an error if not —
// mirroring mod-users' own main.go's fail-fast policy for that mode,
// except this helper returns the error rather than calling os.Exit so
// the caller (a generated app's startup hook) can decide how to react.
//
// Safe to call at every startup: once onboarding is confirmed,
// EnsureSetupToken is a no-op and this returns nil without printing
// anything.
func EnsureSetupTokenAndPrint(ctx context.Context, h *OIDCConfigHandler, tokenDisplay config.TokenDisplay, guiBaseURL string) error {
	if tokenDisplay == config.TokenDisplayNone {
		if !h.CurrentState().Confirmed() {
			return fmt.Errorf("oidc onboarding not confirmed and TOKEN_DISPLAY is none")
		}
		return nil
	}

	// Ensure the setup token is active iff the state calls for it.
	// EnsureSetupToken returns a non-empty plaintext in two cases:
	// first-boot (no prior hash) and restart-with-unconfirmed (prior
	// hash present but the plaintext was unrecoverable, so the token is
	// rotated to give ops a fresh recoverable value). Both cases should
	// trigger a fresh banner/log line.
	plain, err := h.EnsureSetupToken(ctx)
	if err != nil {
		return fmt.Errorf("ensure setup token: %w", err)
	}
	if plain == "" {
		return nil
	}

	switch tokenDisplay {
	case config.TokenDisplayStderr, config.TokenDisplayBoth:
		innerauth.PrintSetupTokenBanner(plain, guiBaseURL+"/oidc-config")
	case config.TokenDisplayLocalhost:
		// Structured log only; the banner is stderr-exclusive.
		slog.ErrorContext(ctx, "oidc onboarding required: setup token ready (use /v1/oidc-config/setup-token from loopback)",
			"setup_token_required", true,
		)
	}
	return nil
}
