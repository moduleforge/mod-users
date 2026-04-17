package config

import (
	"reflect"
	"sort"
	"testing"
)

// Tests are table-driven. Each case declares the raw inputs
// (providers, DB overrides/opt-out, env flag) and the expected verdict
// plus enabled set. The set is sorted before comparison so test
// ordering isn't sensitive to map iteration order.
func TestDetermineBootState(t *testing.T) {
	cases := []struct {
		name           string
		providers      []ProviderInitView
		db             DBConfigView
		envFlag        bool
		wantState      BootState
		wantEnabledSet []string
	}{
		{
			name:           "no env, no flag -> NoEnvNoFlag",
			providers:      nil,
			db:             DBConfigView{},
			envFlag:        false,
			wantState:      BootStateNoEnvNoFlag,
			wantEnabledSet: []string{},
		},
		{
			name:           "no env, env flag set -> ConfirmedOptOut",
			providers:      nil,
			db:             DBConfigView{},
			envFlag:        true,
			wantState:      BootStateConfirmedOptOut,
			wantEnabledSet: []string{},
		},
		{
			name:           "no env, DB opt_out -> ConfirmedOptOut",
			providers:      nil,
			db:             DBConfigView{OptOut: true},
			envFlag:        false,
			wantState:      BootStateConfirmedOptOut,
			wantEnabledSet: []string{},
		},
		{
			name: "env provider inits OK -> ConfirmedOK",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
			},
			wantState:      BootStateConfirmedOK,
			wantEnabledSet: []string{"google"},
		},
		{
			name: "two providers, one fails, one OK -> ConfirmedOK (partial ok)",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
				{ID: "microsoft", Configured: true, Enabled: true, InitOK: false},
			},
			wantState:      BootStateConfirmedOK,
			wantEnabledSet: []string{"google", "microsoft"},
		},
		{
			name: "all providers fail init -> InitFailed",
			providers: []ProviderInitView{
				{ID: "microsoft", Configured: true, Enabled: true, InitOK: false},
			},
			wantState:      BootStateInitFailed,
			wantEnabledSet: []string{"microsoft"},
		},
		{
			name: "DB overrides filter out working google, only broken microsoft remains -> InitFailed",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
				{ID: "microsoft", Configured: true, Enabled: true, InitOK: false},
			},
			db: DBConfigView{
				ProviderOverrides: map[string]bool{"google": false, "microsoft": true},
			},
			wantState:      BootStateInitFailed,
			wantEnabledSet: []string{"microsoft"},
		},
		{
			name: "DB overrides with everything off + opt_out true -> ConfirmedOptOut",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
			},
			db: DBConfigView{
				ProviderOverrides: map[string]bool{"google": false},
				OptOut:            true,
			},
			wantState:      BootStateConfirmedOptOut,
			wantEnabledSet: []string{},
		},
		{
			name: "DB override for a provider missing from env is ignored",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
			},
			db: DBConfigView{
				// "microsoft" no longer in env, but DB remembers it; shouldn't
				// affect the verdict either way.
				ProviderOverrides: map[string]bool{"google": true, "microsoft": true},
			},
			wantState:      BootStateConfirmedOK,
			wantEnabledSet: []string{"google"},
		},
		{
			name: "DB override omits a provider the env configured -> default to enabled",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
				{ID: "authelia", Configured: true, Enabled: true, InitOK: true},
			},
			db: DBConfigView{
				// Operator toggled google explicitly; authelia never
				// touched — leave it on. (This is the "add a new provider
				// to .env without re-confirming" ergonomic we want.)
				ProviderOverrides: map[string]bool{"google": true},
			},
			wantState:      BootStateConfirmedOK,
			wantEnabledSet: []string{"authelia", "google"},
		},
		{
			name: "all providers disabled via DB, opt_out false -> InitFailed (nothing initialized)",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
			},
			db: DBConfigView{
				ProviderOverrides: map[string]bool{"google": false},
				OptOut:            false,
			},
			// This is the edge case the user may hit if they disable
			// everything without setting opt_out. No candidates were
			// filtered out of env, but none made it past the override
			// filter and opt_out isn't set — per algorithm, InitFailed.
			wantState:      BootStateInitFailed,
			wantEnabledSet: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetermineBootState(tc.providers, tc.db, tc.envFlag)
			if got.State != tc.wantState {
				t.Errorf("state: got %q, want %q", got.State, tc.wantState)
			}
			sort.Strings(got.Enabled)
			sort.Strings(tc.wantEnabledSet)
			if !reflect.DeepEqual(got.Enabled, tc.wantEnabledSet) {
				t.Errorf("enabled: got %v, want %v", got.Enabled, tc.wantEnabledSet)
			}
		})
	}
}

// Confirmed() is tiny but public; a quick table test pins the contract
// so future additions to BootState can't silently flip it.
func TestBootState_Confirmed(t *testing.T) {
	cases := []struct {
		state BootState
		want  bool
	}{
		{BootStateConfirmedOK, true},
		{BootStateConfirmedOptOut, true},
		{BootStateInitFailed, false},
		{BootStateNoEnvNoFlag, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			if got := tc.state.Confirmed(); got != tc.want {
				t.Errorf("Confirmed: got %v, want %v", got, tc.want)
			}
		})
	}
}
