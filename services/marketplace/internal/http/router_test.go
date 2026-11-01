package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"qslate/services/marketplace/internal/domain"
	"qslate/services/marketplace/internal/platform/session"
	"qslate/services/marketplace/internal/service"
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

func (s *sessions) Validate(_ context.Context, auth string, uid int64) error {
	s.calls++
	if auth != "Bearer valid" || uid != 42 {
		return session.ErrUnauthorized
	}
	return s.err
}

type repo struct{ domain.Repository }

func (repo) List(context.Context, domain.Filter) (domain.Page, error) {
	return domain.Page{Items: []domain.Item{}}, nil
}
func (repo) Get(context.Context, string) (domain.Item, error) {
	return domain.Item{}, domain.ErrNotFound
}

type artifacts struct{}

func (artifacts) Snapshot(context.Context, string, string, string) (domain.Snapshot, error) {
	return domain.Snapshot{}, domain.ErrUnavailable
}
func routerFor(t *testing.T, sessions *sessions) http.Handler {
	t.Helper()
	cfg, e := service.LoadConfig("../../config/marketplace.json")
	if e != nil {
		t.Fatal(e)
	}
	svc, e := service.New(repo{}, artifacts{}, cfg)
	if e != nil {
		t.Fatal(e)
	}
	return New(svc, cfg, verifier{}, sessions, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func call(h http.Handler, method, path, body, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	req.Header.Set("X-User-ID", "42")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}
func TestPrivateRoutesCheckActiveIdentityBeforePayloads(t *testing.T) {
	s := &sessions{err: session.ErrUnauthorized}
	h := routerFor(t, s)
	for _, route := range []string{"POST /marketplace/items", "PATCH /marketplace/items/id", "DELETE /marketplace/items/id", "POST /marketplace/items/id/install", "DELETE /marketplace/items/id/installation", "POST /marketplace/items/id/reviews", "GET /catalogue/installations", "POST /catalogue/resolve"} {
		parts := strings.SplitN(route, " ", 2)
		for _, bearer := range []string{"", "Bearer forged", "Bearer valid"} {
			res := call(h, parts[0], parts[1], "not JSON", bearer)
			if res.Code != 401 {
				t.Fatalf("auth bypass %s: %d", route, res.Code)
			}
		}
	}
	if s.calls != 8 {
		t.Fatal("unverified JWT reached Auth")
	}
	s.err = session.ErrUnavailable
	if res := call(h, "POST", "/marketplace/items", "{}", "Bearer valid"); res.Code != 503 {
		t.Fatal("Auth outage failed open")
	}
}
func TestPublicBrowsingAndStrictRequests(t *testing.T) {
	s := &sessions{err: session.ErrUnavailable}
	h := routerFor(t, s)
	for _, path := range []string{"/marketplace/items", "/marketplace/featured", "/marketplace/categories"} {
		if r := call(h, "GET", path, "", ""); r.Code != 200 {
			t.Fatalf("public unavailable: %s %d", path, r.Code)
		}
	}
	if s.calls != 0 {
		t.Fatal("public depends on Auth")
	}
	for _, path := range []string{"/marketplace/items?limit=0", "/marketplace/items?limit=1001", "/marketplace/items?limit=1&limit=2", "/marketplace/items?unknown=1", "/marketplace/items?kind=bot", "/marketplace/items?category=unknown", "/marketplace/items?cursor=bad", "/marketplace/categories?cursor=unknown"} {
		if r := call(h, "GET", path, "", ""); r.Code != 400 {
			t.Fatalf("invalid query accepted: %s %d", path, r.Code)
		}
	}
	s.err = nil
	for _, body := range []string{"null", "[]", "{} {}", `{"owner_id":42}`, strings.Repeat("x", 65537)} {
		r := call(h, "POST", "/marketplace/items", body, "Bearer valid")
		if r.Code != 400 && r.Code != 413 {
			t.Fatalf("invalid body accepted %d", r.Code)
		}
	}
}
