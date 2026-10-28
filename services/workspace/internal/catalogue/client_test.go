package catalogue

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"qslate/internal/platform/token"
	"qslate/services/workspace/internal/domain"
	"testing"
)

type verifier struct{}

func (verifier) Verify(value string) (int64, error) { return 42, nil }
func TestCatalogueResolutionFailsClosedAndForwardsVerifiedToken(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{{"resolved", 200, `{"widget_id":"chart","widget_version_id":"v1"}`, nil}, {"missing", 404, `{}`, domain.ErrNotFound}, {"revoked", 401, `{}`, domain.ErrUnauthorized}, {"offline", 503, `{}`, domain.ErrCatalogueUnavailable}, {"wrong version", 200, `{"widget_id":"chart","widget_version_id":"v2"}`, domain.ErrCatalogueUnavailable}, {"redirect", 302, `{}`, domain.ErrCatalogueUnavailable}, {"bad response", 200, `{} {}`, domain.ErrCatalogueUnavailable}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/catalogue/resolve" || r.Header.Get("Authorization") != "Bearer verified" || r.Header.Get("X-User-ID") != "" {
					t.Error("untrusted or missing authority")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if err = client.Validate(context.Background(), 42, "chart", "v1", false); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatal("unverified context accepted")
			}
			var got error
			handler := token.Protect(verifier{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = client.Validate(r.Context(), 42, "chart", "v1", true)
			}))
			request := httptest.NewRequest("POST", "/test", nil)
			request.Header.Set("Authorization", "Bearer verified")
			request.Header.Set("X-User-ID", "99")
			handler.ServeHTTP(httptest.NewRecorder(), request)
			if !errors.Is(got, test.want) {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
}
