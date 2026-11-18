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
	"qslate/services/market-data/internal/domain"
	"qslate/services/market-data/internal/feed"
	"qslate/services/market-data/internal/platform/httpx"
	"qslate/services/market-data/internal/platform/session"
	"qslate/services/market-data/internal/service"
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
	return 0, errors.New("invalid")
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

func (r repo) Get(_ context.Context, owner int64, id string) (domain.Dataset, error) {
	if owner != 41 || id != r.id {
		return domain.Dataset{}, domain.ErrNotFound
	}
	return domain.Dataset{ID: id, VersionID: domain.ID(), Status: "uploaded", Columns: []string{"time", "o", "h", "l", "c"}}, nil
}
func (r repo) Delete(ctx context.Context, owner int64, id string) error {
	_, e := r.Get(ctx, owner, id)
	return e
}
func handler(t *testing.T, live service.LiveConfig) (http.Handler, *sessions, string) {
	t.Helper()
	cfg, e := service.LoadConfig("../../config/market-data.json")
	if e != nil {
		t.Fatal(e)
	}
	if live.MaxSymbols != 0 {
		cfg.Live = live
	}
	id := domain.ID()
	svc, e := service.New(repo{id: id}, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	feeds, e := feed.New(cfg.Live)
	if e != nil {
		t.Fatal(e)
	}
	sessions := &sessions{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpx.Middleware(log, New(svc, feeds, verifier{}, sessions, nil, log)), sessions, id
}
func TestEveryEndpointChecksLiveSessions(t *testing.T) {
	h, s, id := handler(t, service.LiveConfig{})
	paths := []string{"GET /data/symbols", "GET /data/history", "POST /data/custom/upload", "GET /data/custom", "GET /data/custom/" + id, "DELETE /data/custom/" + id, "POST /data/custom/" + id + "/mapping", "POST /data/custom/" + id + "/validate", "POST /catalogue/dataset-snapshot"}
	for _, p := range paths {
		parts := strings.SplitN(p, " ", 2)
		for _, token := range []string{"", "alice"} {
			s.revoked.Store(token != "")
			req := httptest.NewRequest(parts[0], parts[1], nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Fatalf("%s: %d", p, w.Code)
			}
		}
	}
}
func TestOwnershipAndStrictBodies(t *testing.T) {
	h, _, id := handler(t, service.LiveConfig{})
	for _, tc := range []struct {
		method, path, body, access string
		want                       int
	}{{"GET", "/data/custom/" + id, "", "bob", 404}, {"DELETE", "/data/custom/" + id, "", "bob", 404}, {"POST", "/data/custom/" + id + "/mapping", `{"owner_id":41}`, "alice", 400}, {"GET", "/data/history?symbol=x&limit=1&limit=2", "", "alice", 400}, {"POST", "/data/custom/upload", `{}`, "alice", 415}, {"POST", "/catalogue/dataset-snapshot", `{"dataset_id":"invalid","version_id":"invalid"}`, "alice", 400}} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+tc.access)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
}
func TestTickStreamUsesConfiguredSourceAndRevokesSessions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ticks" || r.Header.Get("Authorization") != "Bearer feed-secret" {
			t.Error("wrong source or credential")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []domain.Tick{{ID: "tick-1", Symbol: "TEST", Time: time.Now().UTC(), Price: "10", Quantity: "2"}}})
	}))
	defer upstream.Close()
	t.Setenv("TEST_FEED_URL", upstream.URL)
	t.Setenv("TEST_FEED_TOKEN", "feed-secret")
	live := service.LiveConfig{Enabled: true, OriginEnv: "TEST_FEED_URL", TokenEnv: "TEST_FEED_TOKEN", AllowHTTP: true, PollSeconds: 1, MaxSymbols: 2, MaxStreams: 5, Symbols: []service.Feed{{Symbol: "TEST", Name: "Owned test fixture", OwnerID: 41}}}
	h, s, _ := handler(t, live)
	server := httptest.NewServer(h)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/data/ticks"
	dial := func(token string) *websocket.Conn {
		c, _, e := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://localhost:3000"}})
		if e != nil {
			t.Fatal(e)
		}
		_ = c.WriteJSON(map[string]string{"type": "authenticate", "access_token": token})
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		var ack map[string]any
		if e = c.ReadJSON(&ack); e != nil || ack["type"] != "authenticated" {
			t.Fatal(ack, e)
		}
		return c
	}
	alice := dial("alice")
	defer alice.Close()
	_ = alice.WriteJSON(map[string]any{"type": "subscribe", "symbols": []string{"TEST"}})
	var ack, event map[string]any
	_ = alice.ReadJSON(&ack)
	if e := alice.ReadJSON(&event); e != nil || event["type"] != "market.tick.v1" || event["price"] != "10" {
		t.Fatal(event, e)
	}
	s.revoked.Store(true)
	if _, _, e := alice.ReadMessage(); !websocket.IsCloseError(e, 1008) {
		t.Fatal("revocation did not close stream", e)
	}
	s.revoked.Store(false)
	bob := dial("bob")
	defer bob.Close()
	_ = bob.WriteJSON(map[string]any{"type": "subscribe", "symbols": []string{"TEST"}})
	if _, _, e := bob.ReadMessage(); !websocket.IsCloseError(e, 1008) {
		t.Fatal("private symbol leaked", e)
	}
	if _, resp, e := websocket.DefaultDialer.Dial(url+"?access_token=alice", nil); e == nil || resp.StatusCode != 400 {
		t.Fatal("URL token accepted")
	}
}
