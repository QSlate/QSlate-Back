//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The default stack intentionally has execution disabled. It must not fake a run.
func exerciseLab(t *testing.T, a, b workspaceAPI, alice, bob string) {
	t.Helper()
	a.call("GET", "/backtests", "", nil, 401)
	for _, access := range []string{alice, bob} {
		page := decodeWorkspace[struct {
			Items []any `json:"items"`
		}](t, a.call("GET", "/backtests", access, nil, 200))
		if len(page.Items) != 0 {
			t.Fatal("unexpected owned history")
		}
	}
	id := strings.Repeat("a", 42) + "A" // Canonical base64url ID, not a caller-supplied owner.
	for _, suffix := range []string{"/status", "/summary", "/trades", "/equity-curve", "/logs"} {
		b.call("GET", "/backtests/"+id+suffix, bob, nil, 404)
	}
	a.call("POST", "/backtests/run", alice, map[string]any{"owner_id": 42}, 400)
	input := `{"strategy_id":"` + id + `","strategy_version_id":"` + id + `","dataset_id":"unconfigured","dataset_version_id":"v1","initial_capital":"10000"}`
	req, e := http.NewRequest("POST", a.base+"/qslate/v1/backtests/run", strings.NewReader(input))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+alice)
	req.Header.Set("Idempotency-Key", "lab-default-disabled-0001")
	req.Header.Set("Origin", "http://localhost:3000")
	resp, e := a.client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("disabled execution must fail closed, got %d: %s", resp.StatusCode, body)
	}
}
