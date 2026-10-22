package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"qslate/services/auth/internal/domain"
	"qslate/services/auth/internal/service"
)

type brokenUseCases struct{ UseCases }

func (brokenUseCases) ValidateSession(context.Context, int64, string) error {
	return domain.ErrCredentials
}

func (brokenUseCases) Register(context.Context, string, string, string) (service.Session, error) {
	return service.Session{}, errors.New("private database diagnostic")
}
func (brokenUseCases) Login(context.Context, string, string) (service.Session, error) {
	return service.Session{}, errors.New("private database diagnostic")
}
func (brokenUseCases) Me(context.Context, int64) (domain.User, error) {
	return domain.User{}, errors.New("private database diagnostic")
}

type rejectingVerifier struct{}

func (rejectingVerifier) Verify(string) (int64, error) { return 0, errors.New("invalid token") }

func TestInternalErrorsMaskedAndDirectAccessProtected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := New(brokenUseCases{}, rejectingVerifier{}, func(context.Context) error { return errors.New("database offline") }, logger, Config{AllowedOrigins: []string{"http://localhost:3000"}})
	request := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"identifier":"alice","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 500 || strings.Contains(response.Body.String(), "private database diagnostic") {
		t.Fatal("internal error leaked")
	}
	request = httptest.NewRequest("GET", "/users/me", nil)
	request.Header.Set("X-User-ID", "1")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("service trusts supplied identity header")
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/readyz", nil))
	if response.Code != 503 {
		t.Fatal("readiness ignores failed dependency")
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/healthz", nil))
	if response.Code != 200 {
		t.Fatal("liveness depends on database availability")
	}
}
