package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"qslate/services/marketplace/internal/domain"
	"qslate/services/marketplace/internal/platform/token"
	"strings"
	"testing"
)

type verifier struct{}

func (verifier) Verify(string) (int64, error) { return 42, nil }
func identity(fn func(context.Context)) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer checked")
	token.Protect(verifier{}, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { fn(r.Context()) })).ServeHTTP(httptest.NewRecorder(), r)
}
func TestSnapshotBoundaryAndFailClosedResponses(t *testing.T) {
	status, body := 200, `{"name":"Owned","language":"python","source":"raise RuntimeError('must never execute')","configuration":{}}`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/catalogue/snapshot" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer checked" || r.Header.Get("X-User-ID") != "" {
			t.Error("identity/route forwarding")
		}
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["resource_id"] != "resource" || payload["version_id"] != "version" {
			t.Error("snapshot payload")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer server.Close()
	c, e := New(server.URL, server.URL, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Snapshot(context.Background(), "widget", "resource", "version"); !errors.Is(e, domain.ErrUnauthorized) || calls != 0 {
		t.Fatal("unverified identity forwarded")
	}
	identity(func(ctx context.Context) {
		if _, e := c.Snapshot(ctx, "strategy", "resource", "version"); e != nil {
			t.Fatal(e)
		}
		for _, test := range []struct {
			status int
			body   string
			want   error
		}{{401, "{}", domain.ErrUnauthorized}, {404, "{}", domain.ErrNotFound}, {503, "secret", domain.ErrUnavailable}, {302, "", domain.ErrUnavailable}, {200, `null`, domain.ErrUnavailable}, {200, `{} {}`, domain.ErrUnavailable}, {200, strings.Repeat("x", (1<<20)+1), domain.ErrUnavailable}, {200, `{"name":"Owned","language":"python","source":"pass","configuration":{},"owner_id":1}`, domain.ErrUnavailable}} {
			status, body = test.status, test.body
			if _, e := c.Snapshot(ctx, "widget", "resource", "version"); !errors.Is(e, test.want) {
				t.Fatalf("unsafe response %d: %v", test.status, e)
			}
		}
	})
}
func TestRejectUntrustedArtifactOrigins(t *testing.T) {
	for _, v := range []string{"", "file:///tmp/a", "http://user:secret@host", "https://host/path", "https://host?token=x", "https://host/#fragment"} {
		if _, e := New(v, "https://strategy.example", 1); e == nil {
			t.Fatalf("unsafe origin %s", v)
		}
	}
}
