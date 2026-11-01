package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qslate/services/workspace/internal/domain"
	"qslate/services/workspace/internal/platform/session"
	"qslate/services/workspace/internal/service"
)

type verifier struct{}

func (verifier) Verify(raw string) (int64, error) {
	if raw != "valid" {
		return 0, errors.New("invalid")
	}
	return 42, nil
}

type sessions struct {
	err   error
	calls int
}

func (s *sessions) Validate(_ context.Context, authorization string, owner int64) error {
	s.calls++
	if authorization != "Bearer valid" || owner != 42 {
		return session.ErrUnauthorized
	}
	return s.err
}

type repository struct{ domain.Repository }

func (repository) Create(_ context.Context, d domain.Dashboard) (domain.Dashboard, error) {
	d.Revision = 1
	return d, nil
}
func (repository) Get(context.Context, int64, string) (domain.Dashboard, error) {
	return domain.Dashboard{}, errors.New("postgres password must not leak")
}
func testRouter(t *testing.T, active *sessions) http.Handler {
	t.Helper()
	cfg, err := service.LoadConfig("../../config/layout.json")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(repository{}, cfg, testCatalogue{})
	if err != nil {
		t.Fatal(err)
	}
	return New(svc, verifier{}, active, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestAllBusinessRoutesRequireBearerAndActiveSession(t *testing.T) {
	active := &sessions{}
	router := testRouter(t, active)
	for _, endpoint := range []string{
		"GET /dashboards", "POST /dashboards", "GET /dashboards/id", "PUT /dashboards/id", "DELETE /dashboards/id",
		"POST /dashboards/id/duplicate", "PUT /dashboards/id/layout", "POST /dashboards/id/widgets",
		"PATCH /dashboards/id/widgets/instance", "DELETE /dashboards/id/widgets/instance", "POST /dashboards/id/layout/reset",
	} {
		parts := strings.SplitN(endpoint, " ", 2)
		for _, authorization := range []string{"", "Bearer forged", "Bearer valid"} {
			active.err = session.ErrUnauthorized
			req := httptest.NewRequest(parts[0], parts[1], nil)
			req.Header.Set("Authorization", authorization)
			req.Header.Set("X-User-ID", "42")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != 401 {
				t.Fatalf("%s bypassed authentication: %d", endpoint, response.Code)
			}
		}
	}
	if active.calls != 11 {
		t.Fatalf("signature must be verified before calling Auth: %d", active.calls)
	}
	active.err = session.ErrUnavailable
	req := httptest.NewRequest("GET", "/dashboards", nil)
	req.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != 503 {
		t.Fatal("authority outage must not be accepted or reported as revoked credentials")
	}
}

func TestStrictPayloadsAndSanitizedErrors(t *testing.T) {
	router := testRouter(t, &sessions{})
	id := strings.Repeat("a", 43)
	for _, test := range []struct {
		method, path, body, content string
		status                      int
	}{
		{"POST", "/dashboards", `{"title":"Lab"}`, "application/json", 201},
		{"POST", "/dashboards", `{"title":"Lab","owner_id":999}`, "application/json", 400},
		{"POST", "/dashboards", `{"title":"Lab"}{}`, "application/json", 400},
		{"POST", "/dashboards", `null`, "application/json", 400},
		{"POST", "/dashboards", `{"title":"Lab","description":null}`, "application/json", 400},
		{"POST", "/dashboards", `{"title":"Lab","tags":null}`, "application/json", 400},
		{"POST", "/dashboards", `{"title":"Lab"}`, "text/plain", 415},
		{"POST", "/dashboards", `{"title":"` + strings.Repeat("a", 17000) + `"}`, "application/json", 413},
		{"PUT", "/dashboards/" + id + "/layout", `{"revision":1,"items":[{"instance_id":"x","width":1,"height":1}]}`, "application/json", 400},
		{"PUT", "/dashboards/" + id + "/layout", `{"revision":1,"items":null}`, "application/json", 400},
		{"PATCH", "/dashboards/" + id + "/widgets/" + id, `{}`, "application/json", 400},
		{"PATCH", "/dashboards/" + id + "/widgets/" + id, `{"configuration":null}`, "application/json", 400},
		{"POST", "/dashboards/" + id + "/widgets", `{"widget_id":"w","widget_version_id":"v","configuration":[]}`, "application/json", 400},
		{"GET", "/dashboards?limit=0", "", "", 400}, {"GET", "/dashboards?limit=one", "", "", 400},
		{"GET", "/dashboards?limit=1&limit=2", "", "", 400}, {"GET", "/dashboards?owner_id=999", "", "", 400},
		{"GET", "/dashboards/" + id, "", "", 500},
	} {
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		req.Header.Set("Authorization", "Bearer valid")
		req.Header.Set("Content-Type", test.content)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != test.status || strings.Contains(response.Body.String(), "password") {
			t.Fatalf("%s %s: got %d, want %d: %s", test.method, test.path, response.Code, test.status, response.Body.String())
		}
		if test.status == 201 && (strings.Contains(response.Body.String(), "owner_id") || !strings.Contains(response.Body.String(), `"widgets":[]`)) {
			t.Fatal("invalid public dashboard response")
		}
	}
}
