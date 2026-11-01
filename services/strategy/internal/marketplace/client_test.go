package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"qslate/services/strategy/internal/platform/token"
	"strings"
	"testing"
)

type verifier struct{}

func (verifier) Verify(string) (int64, error) { return 42, nil }
func identity(fn func(context.Context)) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer checked")
	token.Protect(verifier{}, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { fn(r.Context()) })).ServeHTTP(httptest.NewRecorder(), req)
}
func TestPrivateEntitlementBoundary(t *testing.T) {
	key, version := strings.Repeat("a", 43), strings.Repeat("v", 43)
	ref := "market-" + key
	status := 200
	bad := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer checked" || r.Header.Get("X-User-ID") != "" {
			t.Error("unverified identity forwarding")
		}
		w.WriteHeader(status)
		if status != 200 {
			return
		}
		if bad {
			w.Write([]byte(`{"items":[],"next_cursor":"forged"}`))
			return
		}
		summary := Summary{ItemID: key, VersionID: version, Kind: "widget", CatalogID: ref, Name: "Pinned", Language: "python"}
		if r.URL.Path == "/catalogue/installations" {
			json.NewEncoder(w).Encode(Page{Items: []Summary{summary}})
		} else if r.URL.Path == "/catalogue/resolve" {
			json.NewEncoder(w).Encode(Installation{Summary: summary, Source: "raise RuntimeError('never execute')", Configuration: json.RawMessage(`{}`)})
		} else {
			t.Error("unexpected route")
		}
	}))
	defer server.Close()
	c, e := New(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.List(context.Background(), "widgets", "", 100); !errors.Is(e, ErrUnauthorized) || calls != 0 {
		t.Fatal("missing verified identity")
	}
	identity(func(ctx context.Context) {
		p, e := c.List(ctx, "widgets", "", 100)
		if e != nil || len(p.Items) != 1 || p.Items[0].CatalogID != ref {
			t.Fatal("catalogue", e)
		}
		v, e := c.Resolve(ctx, ref, version, false)
		if e != nil || v.VersionID != version {
			t.Fatal("resolve", e)
		}
		before := calls
		if p, e = c.List(ctx, "widgets", strings.Repeat("A", 43), 100); e != nil || len(p.Items) != 0 || calls != before {
			t.Fatal("cross-namespace cursor handling")
		}
		bad = true
		if _, e = c.List(ctx, "widgets", "", 100); !errors.Is(e, ErrUnavailable) {
			t.Fatal("invalid pagination accepted")
		}
		bad = false
		for _, s := range []int{401, 404, 503, 302} {
			status = s
			_, e = c.Resolve(ctx, ref, version, false)
			want := ErrUnavailable
			if s == 401 {
				want = ErrUnauthorized
			}
			if s == 404 {
				want = ErrNotFound
			}
			if !errors.Is(e, want) {
				t.Fatalf("status mapping %d %v", s, e)
			}
		}
	})
}
func TestOptionalAndInvalidOrigins(t *testing.T) {
	c, e := New("")
	if e != nil || c != nil {
		t.Fatal("optional boundary")
	}
	for _, origin := range []string{"file:///tmp/a", "http://user:secret@host", "https://host/path", "https://host?key=x"} {
		if _, e = New(origin); e == nil {
			t.Fatal("untrusted origin")
		}
	}
}
