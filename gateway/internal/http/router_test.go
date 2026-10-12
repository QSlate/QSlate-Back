package http

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qslate/gateway/internal/config"
)

type verifier struct{}

func (verifier) Verify(raw string) (int64, error) {
	if raw == "valid" {
		return 42, nil
	}
	return 0, errors.New("invalid token")
}

func TestGatewayPolicyAndProxy(t *testing.T) {
	var received *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(200)
			return
		}
		received = r.Clone(r.Context())
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := []config.Route{{Method: "GET", Path: "/api/auth/me", Upstream: upstream.URL, TargetPath: "/auth/me", AuthRequired: true}, {Method: "POST", Path: "/api/auth/login", Upstream: upstream.URL, TargetPath: "/auth/login", RateLimited: true}}
	router, err := New(routes, verifier{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "Bearer forged", "Basic valid"} {
		request := httptest.NewRequest("GET", "/api/auth/me", nil)
		request.Header.Set("Authorization", raw)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 401 || received != nil {
			t.Fatal("unauthorized request reached upstream")
		}
	}
	request := httptest.NewRequest("GET", "/api/auth/me?limit=1", nil)
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("X-User-ID", "999")
	request.Header.Set("X-Internal-Secret", "spoofed")
	request.Header.Set("X-Forwarded-For", "spoofed")
	request.Header.Set("Forwarded", "spoofed")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || received.URL.Path != "/auth/me" || received.URL.RawQuery != "limit=1" {
		t.Fatal("rewrite failed")
	}
	if received.Header.Get("X-User-ID") != "" || received.Header.Get("X-Internal-Secret") != "" || strings.Contains(received.Header.Get("X-Forwarded-For"), "spoofed") || received.Header.Get("Forwarded") != "" {
		t.Fatal("spoofed identity headers propagated")
	}
	for _, test := range []struct {
		method, path string
		status       int
	}{{"POST", "/api/auth/me", 405}, {"GET", "/api/auth/me/extra", 404}, {"GET", "/api/auth/login", 405}, {"GET", "/readyz", 200}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s %s status=%d want=%d", test.method, test.path, response.Code, test.status)
		}
	}
	for index := 0; index < 11; index++ {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/api/auth/login", nil))
		if index == 10 && (response.Code != 429 || response.Header().Get("Retry-After") == "") {
			t.Fatal("auth limit not enforced")
		}
	}
}

func TestGatewayRejectsDuplicateRoutesAndReturns502(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	upstream := httptest.NewServer(http.NotFoundHandler())
	upstream.Close()
	route := config.Route{Method: "POST", Path: "/api/auth/login", Upstream: upstream.URL, TargetPath: "/auth/login"}
	if _, err := New([]config.Route{route, route}, verifier{}, logger); err == nil {
		t.Fatal("duplicate route accepted")
	}
	router, err := New([]config.Route{route}, verifier{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/api/auth/login", nil))
	if response.Code != 502 {
		t.Fatalf("status=%d want=502", response.Code)
	}
}
