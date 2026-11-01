package analysis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"qslate/services/strategy/internal/domain"
	"qslate/services/strategy/internal/service"
	"strings"
	"testing"
)

func TestAnalysisResponsesAndNoCredentialForwarding(t *testing.T) {
	for _, test := range []struct {
		body   string
		status int
		valid  bool
		ok     bool
	}{
		{`{"valid":true,"diagnostics":[]}`, 200, true, true},
		{`{"valid":false,"diagnostics":[{"line":1,"column":2,"end_line":1,"end_column":3,"severity":"error","message":"invalid syntax"}]}`, 200, false, true},
		{`{"valid":false,"diagnostics":[]}`, 200, false, false},
		{`{"valid":true,"diagnostics":null}`, 200, false, false},
		{`{"valid":true}`, 200, false, false},
		{`{"valid":true,"diagnostics":[]} {}`, 200, false, false},
		{`{"valid":true,"diagnostics":[],"secret":"no"}`, 200, false, false},
		{strings.Repeat("x", 16385), 200, false, false},
		{`{}`, 503, false, false}, {`{}`, 302, false, false},
	} {
		t.Run(test.body[:min(len(test.body), 35)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("X-User-ID") != "" {
					t.Error("credential forwarded")
				}
				w.Header().Set("Location", "/other")
				w.WriteHeader(test.status)
				w.Write([]byte(test.body))
			}))
			defer server.Close()
			t.Setenv("TEST_ANALYZER_URL", server.URL)
			client, err := New(service.AnalysisConfig{Enabled: true, URLEnv: "TEST_ANALYZER_URL", TimeoutSeconds: 1, AllowInsecureHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			d, err := client.Check(context.Background(), domain.LintInput{Source: "x", Language: "python"})
			if test.ok && (err != nil || d.Valid != test.valid) {
				t.Fatal(err)
			}
			if !test.ok && !errors.Is(err, domain.ErrUnavailable) {
				t.Fatal("unexpected response accepted")
			}
		})
	}
}
func TestAnalysisOriginValidation(t *testing.T) {
	for _, origin := range []string{"http://localhost:1234", "https://user:password@example.com", "https://example.com/execute", "https://example.com/?secret=x", ""} {
		t.Setenv("TEST_ANALYZER_URL", origin)
		if _, err := New(service.AnalysisConfig{Enabled: true, URLEnv: "TEST_ANALYZER_URL", TimeoutSeconds: 1}); err == nil {
			t.Fatal("invalid or insecure origin")
		}
	}
	if client, err := New(service.AnalysisConfig{}); client != nil || err != nil {
		t.Fatal("disabled analyzer")
	}
}
