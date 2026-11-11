package http

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/platform/httpx"
	"qslate/services/lab/internal/platform/session"
	"qslate/services/lab/internal/service"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type verifier struct{}

func (verifier) Verify(s string) (int64, error) {
	if s == "alice" {
		return 41, nil
	}
	if s == "bob" {
		return 42, nil
	}
	return 0, errors.New("invalid token")
}

type sessions struct{ revoked atomic.Bool }

func (s *sessions) Validate(context.Context, string, int64) error {
	if s.revoked.Load() {
		return session.ErrUnauthorized
	}
	return nil
}

type repo struct {
	service.Repository
	id string
}

func (r repo) Get(_ context.Context, uid int64, id string) (domain.Job, error) {
	if uid != 41 || id != r.id {
		return domain.Job{}, domain.ErrNotFound
	}
	return domain.Job{ID: id, Status: "running", Input: domain.RunInput{InitialCapital: "10000"}}, nil
}
func (r repo) Events(ctx context.Context, uid int64, id string, after int64) ([]domain.Event, error) {
	if _, e := r.Get(ctx, uid, id); e != nil {
		return nil, e
	}
	if after >= 1 {
		return []domain.Event{}, nil
	}
	return []domain.Event{{ID: 1, Type: "progress", Data: json.RawMessage(`{"progress":42}`)}}, nil
}
func (r repo) Result(ctx context.Context, uid int64, id string) (domain.Result, error) {
	if _, e := r.Get(ctx, uid, id); e != nil {
		return domain.Result{}, e
	}
	return domain.Result{}, domain.ErrConflict
}
func fixture(t *testing.T) (http.Handler, *sessions, string) {
	t.Helper()
	cfg, e := service.LoadConfig("../../config/lab.json")
	if e != nil {
		t.Fatal(e)
	}
	id := domain.ID()
	svc, e := service.New(repo{id: id}, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	sessions := &sessions{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpx.Middleware(logger, New(svc, verifier{}, sessions, nil, logger)), sessions, id
}
func TestEveryRESTOperationRequiresLiveAuthentication(t *testing.T) {
	h, s, id := fixture(t)
	for _, test := range []struct{ method, path string }{{"POST", "/backtests/run"}, {"POST", "/backtests/optimize"}, {"POST", "/backtests/compare"}, {"GET", "/backtests"}, {"GET", "/backtests/" + id + "/status"}, {"POST", "/backtests/" + id + "/cancel"}, {"DELETE", "/backtests/" + id}, {"GET", "/backtests/" + id + "/summary"}, {"GET", "/backtests/" + id + "/trades"}, {"GET", "/backtests/" + id + "/equity-curve"}, {"GET", "/backtests/" + id + "/logs"}} {
		for _, access := range []string{"", "alice"} {
			s.revoked.Store(access != "")
			w := httptest.NewRecorder()
			req := httptest.NewRequest(test.method, test.path, nil)
			if access != "" {
				req.Header.Set("Authorization", "Bearer "+access)
			}
			h.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Fatalf("%s %s got %d", test.method, test.path, w.Code)
			}
		}
	}
}
func TestOwnershipAndCursorValidation(t *testing.T) {
	h, _, id := fixture(t)
	for _, tc := range []struct {
		path, access string
		status       int
	}{{"/backtests/" + id + "/status", "bob", 404}, {"/backtests/" + id + "/status", "alice", 200}, {"/backtests/" + id + "/logs?cursor=-1", "alice", 400}, {"/backtests/" + id + "/logs?limit=1&limit=2", "alice", 400}, {"/backtests/" + id + "/summary", "alice", 409}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+tc.access)
		h.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
}
func TestWebSocketFirstFrameOwnershipReplayAndRevocation(t *testing.T) {
	h, s, id := fixture(t)
	server := httptest.NewServer(h)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/backtests/" + id + "/stream"
	dial := func(access string, after int64) *websocket.Conn {
		t.Helper()
		conn, _, e := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://localhost:3000"}})
		if e != nil {
			t.Fatal(e)
		}
		if e = conn.WriteJSON(map[string]any{"type": "authenticate", "access_token": access, "after_event_id": after}); e != nil {
			t.Fatal(e)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		return conn
	}
	bob := dial("bob", 0)
	if _, _, e := bob.ReadMessage(); !websocket.IsCloseError(e, 1008) {
		t.Fatalf("owner check: %v", e)
	}
	bob.Close()
	alice := dial("alice", 0)
	defer alice.Close()
	var event domain.Event
	if e := alice.ReadJSON(&event); e != nil || event.ID != 1 {
		t.Fatalf("event: %+v %v", event, e)
	}
	s.revoked.Store(true)
	if _, _, e := alice.ReadMessage(); !websocket.IsCloseError(e, 1008) {
		t.Fatalf("revocation: %v", e)
	}
	s.revoked.Store(false)
	resumed := dial("alice", 1)
	defer resumed.Close()
	s.revoked.Store(true)
	if _, _, e := resumed.ReadMessage(); !websocket.IsCloseError(e, 1008) {
		t.Fatalf("resume replayed an old event: %v", e)
	}
	if _, resp, e := websocket.DefaultDialer.Dial(url+"?access_token=alice", nil); e == nil || resp.StatusCode != 400 {
		t.Fatal("URL token accepted")
	}
	if _, resp, e := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"https://untrusted.invalid"}}); e == nil || resp.StatusCode != 403 {
		t.Fatal("untrusted origin accepted")
	}
}
