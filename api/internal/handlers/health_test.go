package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestReady(t *testing.T) {
	const secret = "dial tcp 10.0.0.5:5432 user=secret db=prod"

	rec := httptest.NewRecorder()
	Ready(fakePinger{err: errors.New(secret)})(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["db"] != "unavailable" {
		t.Errorf("db = %q, want unavailable", body["db"])
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") || strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("body leaks error detail: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	Ready(fakePinger{})(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["db"] != "ok" {
		t.Errorf("db = %q, want ok", body["db"])
	}
}
