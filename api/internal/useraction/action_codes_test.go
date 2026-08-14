package useraction

import (
	"net/http"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
)

func TestActionCodes(t *testing.T) {
	tests := []struct {
		name       string
		got        apiresp.ActionCode
		wantCode   string
		wantStatus int
	}{
		{
			name:       "EmailUnverified",
			got:        EmailUnverified,
			wantCode:   "users.email_unverified",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "StepUpRequired",
			got:        StepUpRequired,
			wantCode:   "users.step_up_required",
			wantStatus: http.StatusConflict,
		},
		{
			name:       "OIDCNotConfirmed",
			got:        OIDCNotConfirmed,
			wantCode:   "users.oidc_not_confirmed",
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", tt.got.Code, tt.wantCode)
			}
			if tt.got.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", tt.got.Status, tt.wantStatus)
			}
		})
	}
}
