package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAccessLog_ForwardsFlusher guards against statusWriter losing the
// underlying ResponseWriter's http.Flusher support. httptest.ResponseRecorder
// implements http.Flusher, so if AccessLog's wrapper fails to forward it,
// this test fails.
func TestAccessLog_ForwardsFlusher(t *testing.T) {
	rec := httptest.NewRecorder()

	handler := AccessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected ResponseWriter passed to handler to implement http.Flusher")
		}
		flusher.Flush()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if !rec.Flushed {
		t.Fatal("expected underlying ResponseRecorder to observe a Flush call")
	}
}
