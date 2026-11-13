package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseOIDCSigningAlgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw     string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"  ", nil, false},
		{"EdDSA", []string{"EdDSA"}, false},
		{" EdDSA , RS256 ,EdDSA", []string{"EdDSA", "RS256"}, false},
		{"none", nil, true},
		{"HS256", nil, true},
		{"RS256,HS512", nil, true},
		{"rs256", nil, true},
		{"EdDSA,,RS256", nil, true},
		{"EdDSA,", nil, true},
	}
	for _, tc := range tests {
		got, err := ParseOIDCSigningAlgs(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Errorf("%q: err = %v, wantErr %v", tc.raw, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestOIDCSigningAlgAllowlist_ExcludesForbidden(t *testing.T) {
	t.Parallel()
	for _, a := range OIDCSigningAlgAllowlist() {
		if a == "none" || strings.HasPrefix(a, "HS") {
			t.Errorf("allowlist contains forbidden algorithm %q", a)
		}
	}
	if !IsAllowedOIDCSigningAlg("EdDSA") {
		t.Error("EdDSA not allowed")
	}
}

func TestLoadOIDCAllowedSigningAlgs(t *testing.T) {
	t.Setenv(OIDCAllowedSigningAlgsEnv, "EdDSA,ES256")
	algs, problems := loadOIDCAllowedSigningAlgs()
	if len(problems) != 0 || !reflect.DeepEqual(algs, []string{"EdDSA", "ES256"}) {
		t.Errorf("got %v, %v", algs, problems)
	}
	t.Setenv(OIDCAllowedSigningAlgsEnv, "EdDSA,HS256")
	algs, problems = loadOIDCAllowedSigningAlgs()
	if algs != nil || len(problems) != 1 || !strings.HasPrefix(problems[0], OIDCAllowedSigningAlgsEnv+": ") {
		t.Errorf("got %v, %v", algs, problems)
	}
	t.Setenv(OIDCAllowedSigningAlgsEnv, "")
	if algs, problems = loadOIDCAllowedSigningAlgs(); algs != nil || len(problems) != 0 {
		t.Errorf("unset: got %v, %v", algs, problems)
	}
}
