package marketdata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"qslate/services/lab/internal/platform/token"
	"testing"
)

type verifier struct{}

func (verifier) Verify(string) (int64, error) { return 41, nil }
func TestSnapshotAuthorizationIdentityChecksumAndBounds(t *testing.T) {
	csv := "Datetime,Open,High,Low,Close,Volume\n2024-01-01T00:00:00Z,1,3,1,2,0\n"
	sum := sha256.Sum256([]byte(csv))
	bad := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalogue/dataset-snapshot" || r.Header.Get("Authorization") != "Bearer verified" {
			t.Error("verified identity not forwarded")
		}
		s := hex.EncodeToString(sum[:])
		if bad {
			s = "wrong"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"dataset_id": "id", "version_id": "version", "csv": csv, "sha256": s})
	}))
	defer server.Close()
	client, e := New(server.URL, 4096)
	if e != nil {
		t.Fatal(e)
	}
	h := token.Protect(verifier{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _, e := client.Snapshot(r.Context(), "id", "version")
		if bad {
			if e == nil {
				t.Error("checksum ignored")
			}
		} else if e != nil || got != csv {
			t.Error(got, e)
		}
	}))
	for _, v := range []bool{false, true} {
		bad = v
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer verified")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if c, e := New("", 4096); e != nil || c != nil {
		t.Fatal("local standalone adapter unavailable")
	}
}
