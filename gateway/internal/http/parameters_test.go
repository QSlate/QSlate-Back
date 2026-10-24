package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"qslate/gateway/internal/config"
)

func TestParameterizedRoutesPreserveIdentifiersAndRejectEncodedSlashes(t *testing.T) {
	var path, query string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		query = r.URL.RawQuery
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	routes := []config.Route{{Method: "DELETE", Path: "/qslate/v1/users/me/api-keys/{keyId}", TargetPath: "/users/me/api-keys/{keyId}", Upstream: upstream.URL}, {Method: "GET", Path: "/qslate/v1/exports/{runId}/trades.csv", TargetPath: "/exports/{runId}/trades.csv", Upstream: upstream.URL}}
	router, err := New(routes, verifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("DELETE", "/qslate/v1/users/me/api-keys/key_123?test=1", nil))
	if w.Code != 204 || path != "/users/me/api-keys/key_123" || query != "test=1" {
		t.Fatalf("parameter rewrite failed: status=%d path=%s query=%s", w.Code, path, query)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/qslate/v1/exports/run1/trades.csv", nil))
	if w.Code != 204 || path != "/exports/run1/trades.csv" {
		t.Fatal("dotted route failed")
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("DELETE", "/qslate/v1/users/me/api-keys/a%2Fb", nil))
	if w.Code != 400 {
		t.Fatal("encoded slash accepted as identifier")
	}
}
