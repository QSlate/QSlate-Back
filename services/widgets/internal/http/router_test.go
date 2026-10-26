package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"qslate/services/widgets/internal/domain"
	"qslate/services/widgets/internal/platform/session"
	"qslate/services/widgets/internal/service"
	"strings"
	"testing"
)

type verifier struct{}

func (verifier) Verify(raw string) (int64, error) {
	if raw != "valid" {
		return 0, errors.New("bad token")
	}
	return 42, nil
}

type sessions struct {
	err   error
	calls int
}

func (s *sessions) Validate(_ context.Context, authorization string, uid int64) error {
	s.calls++
	if authorization != "Bearer valid" || uid != 42 {
		return session.ErrUnauthorized
	}
	return s.err
}

type repo struct{ domain.Repository }

func (repo) Create(_ context.Context, uid int64, wid, version string, in domain.Input) (domain.Widget, error) {
	return domain.Widget{Summary: domain.Summary{ID: wid, VersionID: version, OwnerID: uid, Name: in.Name, Language: in.Language, Kind: "custom"}, Source: in.Source, Configuration: in.Configuration}, nil
}
func (repo) Get(context.Context, int64, string) (domain.Widget, error) {
	return domain.Widget{}, errors.New("database password must not leak")
}
func routerFor(t *testing.T, active *sessions) http.Handler {
	t.Helper()
	cfg, err := service.LoadConfig("../../config/catalogue.json")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(repo{}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(svc, cfg, verifier{}, active, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func TestAllWidgetsOperationsRequireActiveIdentity(t *testing.T) {
	active := &sessions{err: session.ErrUnauthorized}
	handler := routerFor(t, active)
	for _, path := range []string{"GET /widgets/installed", "GET /widgets/custom", "POST /widgets/custom", "GET /widgets/custom/id", "PUT /widgets/custom/id", "DELETE /widgets/custom/id", "POST /widgets/custom/id/preview", "POST /catalogue/resolve"} {
		parts := strings.SplitN(path, " ", 2)
		for _, bearer := range []string{"", "Bearer forged", "Bearer valid"} {
			request := httptest.NewRequest(parts[0], parts[1], nil)
			request.Header.Set("Authorization", bearer)
			request.Header.Set("X-User-ID", "42")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 401 {
				t.Fatalf("authentication bypass %s: %d", path, response.Code)
			}
		}
	}
	if active.calls != 8 {
		t.Fatal("unverified token reached Auth")
	}
	active.err = session.ErrUnavailable
	request := httptest.NewRequest("GET", "/widgets/installed", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatal("Auth outage did not fail closed")
	}
}
func TestSourceSpecificBodyLimitsAndStrictJSON(t *testing.T) {
	handler := routerFor(t, &sessions{})
	for _, test := range []struct {
		method, path, body, content string
		status                      int
	}{
		{"POST", "/widgets/custom", `{"name":"Chart","language":"python","source":"` + strings.Repeat("x", 50000) + `"}`, "application/json", 201},
		{"POST", "/widgets/custom", `{"name":"Chart","language":"python","source":"x","owner_id":1}`, "application/json", 400},
		{"POST", "/widgets/custom", `{"name":"Chart","language":"python","source":"x","configuration":null}`, "application/json", 400},
		{"POST", "/widgets/custom", `{} {}`, "application/json", 400}, {"POST", "/widgets/custom", `null`, "application/json", 400},
		{"POST", "/widgets/custom", `{"name":"Chart","language":"go","source":"x"}`, "application/json", 400},
		{"POST", "/widgets/custom", `{"name":"Chart","language":"python","source":"` + strings.Repeat("x", 1100000) + `"}`, "application/json", 413},
		{"POST", "/widgets/custom", `{}`, "text/plain", 415}, {"GET", "/widgets/custom?limit=0", "", "", 400}, {"GET", "/widgets/custom?limit=1&limit=2", "", "", 400},
		{"GET", "/widgets/custom/" + strings.Repeat("a", 43), "", "", 500},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer valid")
		request.Header.Set("Content-Type", test.content)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || strings.Contains(response.Body.String(), "password") || strings.Contains(response.Body.String(), "owner_id") {
			t.Fatalf("%s %s: got %d want %d", test.method, test.path, response.Code, test.status)
		}
	}
}
