package strategy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"qslate/services/lab/internal/platform/token"
	"testing"
)

type verifier struct{}

func (verifier) Verify(string) (int64, error) { return 41, nil }
func TestSnapshotUsesVerifiedBearerAndPreservesParameterNumbers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalogue/execution-snapshot" || r.Header.Get("Authorization") != "Bearer verified" {
			t.Error("unverified request")
		}
		io.WriteString(w, `{"language":"python","source":"class S(Strategy): pass","configuration":{"big":9007199254740993}}`)
	}))
	defer upstream.Close()
	client, e := New(upstream.URL)
	if e != nil {
		t.Fatal(e)
	}
	h := token.Protect(verifier{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, e := client.Snapshot(r.Context(), "id", "version")
		if e != nil || out.Parameters["big"] != json.Number("9007199254740993") {
			t.Fatal(out, e)
		}
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer verified")
	h.ServeHTTP(httptest.NewRecorder(), req)
	for _, origin := range []string{"", "http://user:password@localhost", "https://example.org/path", "https://example.org?secret=yes"} {
		if _, e = New(origin); e == nil {
			t.Fatal("invalid origin accepted")
		}
	}
}
