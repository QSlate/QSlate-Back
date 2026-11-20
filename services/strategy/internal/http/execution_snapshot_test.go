package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"qslate/services/strategy/internal/domain"
	"qslate/services/strategy/internal/marketplace"
	"qslate/services/strategy/internal/service"
	"strings"
	"testing"
)

type executionRepo struct{ domain.Repository }

func (executionRepo) Snapshot(_ context.Context, owner int64, id, version string) (domain.Input, error) {
	if owner != 42 {
		return domain.Input{}, domain.ErrNotFound
	}
	return domain.Input{Name: "Pinned", Source: "class Own(Strategy): pass", Parameters: json.RawMessage(`{"period":10}`)}, nil
}
func TestExecutionSnapshotUsesOwnedOrActiveInstalledPinnedStrategy(t *testing.T) {
	installed := true
	kind := "strategy"
	item := strings.Repeat("a", 43)
	version := strings.Repeat("b", 43)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			ID       string `json:"catalog_id"`
			Version  string `json:"version_id"`
			Existing bool   `json:"existing"`
		}
		if e := json.NewDecoder(req.Body).Decode(&in); e != nil {
			t.Error(e)
		}
		if req.URL.Path != "/catalogue/resolve" || req.Header.Get("Authorization") != "Bearer valid" || in.Existing || in.Version != version {
			t.Error("execution reused a retained grant or lost authorization/version")
		}
		if !installed {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"item_id": item, "version_id": version, "catalog_id": "market-" + item, "kind": kind, "name": "Shared", "language": "python", "source": "class Shared(Strategy): pass", "configuration": map[string]any{}, "installed_at": "2024-01-01T00:00:00Z"})
	}))
	defer upstream.Close()
	client, e := marketplace.New(upstream.URL)
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := service.LoadConfig("../../config/strategy.json")
	if e != nil {
		t.Fatal(e)
	}
	svc, e := service.New(executionRepo{}, cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	h := New(svc, cfg, verifier{}, &sessions{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), client)
	call := func(id, vid string, want int) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"resource_id": id, "version_id": vid})
		res := request(h, "POST", "/catalogue/execution-snapshot", "application/json", strings.NewReader(string(body)))
		if res.Code != want {
			t.Fatalf("snapshot %s got %d: %s", id, res.Code, res.Body.String())
		}
	}
	call(item, version, 200)
	call("market-"+item, version, 200)
	call("market-"+item, "", 400)
	installed = false
	call("market-"+item, version, 404)
	installed = true
	kind = "widget"
	call("market-"+item, version, 404)
}
