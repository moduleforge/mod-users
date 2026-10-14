package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newOIDCTestServer(t *testing.T, tlsServer bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q,"authorization_endpoint":%q,"token_endpoint":%q,"id_token_signing_alg_values_supported":["RS256"]}`,
			srv.URL, srv.URL+"/jwks", srv.URL+"/auth", srv.URL+"/token")
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"keys":[]}`)
	})
	if tlsServer {
		srv = httptest.NewTLSServer(mux)
	} else {
		srv = httptest.NewServer(mux)
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestNewVerifierWithOptions_TLSVerifiedByDefault(t *testing.T) {
	srv := newOIDCTestServer(t, true)
	_, err := NewVerifierWithOptions(context.Background(), WithOIDC(srv.URL, "cid"))
	if err == nil {
		t.Fatal("expected discovery failure for untrusted certificate")
	}
	if !strings.Contains(err.Error(), "discovery") {
		t.Fatalf("error should mention discovery: %v", err)
	}
	if !strings.Contains(err.Error(), "x509") && !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("error should mention x509 or certificate (TLS verification failure): %v", err)
	}
}

func TestNewVerifier_TLSVerifiedAlways(t *testing.T) {
	srv := newOIDCTestServer(t, true)
	if _, err := NewVerifier(context.Background(), srv.URL, "cid", "", ""); err == nil {
		t.Fatal("expected failure for untrusted certificate")
	}
}

func TestNewVerifierWithOptions_InsecureSkipTLSVerifyOptIn(t *testing.T) {
	srv := newOIDCTestServer(t, true)
	v, err := NewVerifierWithOptions(context.Background(), WithOIDC(srv.URL, "cid"), WithInsecureSkipTLSVerify())
	if err != nil {
		t.Fatalf("opt-in should succeed: %v", err)
	}
	if v.oidcVerifier == nil {
		t.Fatal("expected OIDC verifier")
	}
}

func TestNewVerifierWithOptions_InsecureOptInIgnoredForHTTP(t *testing.T) {
	srv := newOIDCTestServer(t, false)
	if _, err := NewVerifierWithOptions(context.Background(), WithOIDC(srv.URL, "cid"), WithInsecureSkipTLSVerify()); err != nil {
		t.Fatalf("http issuer should work: %v", err)
	}
}
