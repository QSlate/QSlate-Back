//go:build e2e

package e2e

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Reuse the existing two-account flow to avoid consuming extra Auth rate limits.
func exerciseStrategies(t *testing.T, a, b workspaceAPI, alice, bob string) {
	t.Helper()
	type strategy struct {
		ID        string `json:"id"`
		VersionID string `json:"version_id"`
		Revision  int64  `json:"revision"`
		Name      string `json:"name"`
		Source    string `json:"source"`
	}
	input := map[string]any{"name": "Mean reversion", "source": "def run(data):\n    return data\n", "parameters": map[string]int{"period": 20}}
	a.call("GET", "/strategies", "", nil, 401)
	a.call("POST", "/strategies", alice, map[string]string{"name": "stolen", "source": "pass", "owner_id": "42"}, 400)
	first := decodeWorkspace[strategy](t, a.call("POST", "/strategies", alice, input, 201))
	if first.Revision != 1 || len(first.ID) != 43 || len(first.VersionID) != 43 {
		t.Fatal("invalid strategy identity")
	}
	path := "/strategies/" + first.ID
	updatedInput := map[string]any{"name": "Updated strategy", "source": "def run(data):\n    return []\n", "parameters": map[string]int{"period": 50}, "base_version_id": first.VersionID}
	for _, action := range []struct {
		method, suffix string
		body           any
	}{
		{"GET", "", nil}, {"PUT", "", updatedInput}, {"DELETE", "", nil}, {"GET", "/versions", nil}, {"POST", "/restore", map[string]string{"version_id": first.VersionID, "base_version_id": first.VersionID}},
	} {
		b.call(action.method, path+action.suffix, bob, action.body, 404)
	}
	bobPage := decodeWorkspace[struct {
		Items []strategy `json:"items"`
	}](t, b.call("GET", "/strategies", bob, nil, 200))
	if len(bobPage.Items) != 0 {
		t.Fatal("strategy catalogue leaked")
	}
	current := decodeWorkspace[strategy](t, a.call("PUT", path, alice, updatedInput, 200))
	if current.Revision != 2 || current.VersionID == first.VersionID {
		t.Fatal("save not versioned")
	}
	a.call("PUT", path, alice, updatedInput, 409)
	a.call("POST", path+"/restore", alice, map[string]string{"version_id": first.VersionID, "base_version_id": first.VersionID}, 409)
	restored := decodeWorkspace[strategy](t, a.call("POST", path+"/restore", alice, map[string]string{"version_id": first.VersionID, "base_version_id": current.VersionID}, 200))
	if restored.Revision != 3 || restored.VersionID == first.VersionID || restored.Source != first.Source || restored.Name != first.Name {
		t.Fatal("restore did not create copied snapshot")
	}
	history := decodeWorkspace[struct {
		Items []strategy `json:"items"`
		Next  string     `json:"next_cursor"`
	}](t, a.call("GET", path+"/versions?limit=1", alice, nil, 200))
	if len(history.Items) != 1 || history.Items[0].Revision != 3 || history.Next != "3" {
		t.Fatal("version pagination failed")
	}
	a.call("GET", path+"/versions?cursor="+url.QueryEscape(history.Next)+"&limit=20", alice, nil, 200)
	a.call("GET", path+"/versions?limit=21", alice, nil, 400)
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	form.WriteField("name", "Imported strategy")
	file, _ := form.CreateFormFile("file", "imported.py")
	io.WriteString(file, "raise RuntimeError('must never execute')\n")
	form.Close()
	req, err := http.NewRequest("POST", a.base+"/qslate/v1/strategies/import", &upload)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+alice)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Origin", "http://localhost:3000")
	response, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if err != nil || response.StatusCode != 201 {
		t.Fatalf("import failed: %d %v", response.StatusCode, err)
	}
	imported := decodeWorkspace[strategy](t, raw)
	pageRaw := a.call("GET", "/strategies?limit=1", alice, nil, 200)
	if bytes.Contains(pageRaw, []byte(`"source"`)) {
		t.Fatal("source leaked in summary")
	}
	page := decodeWorkspace[struct {
		Items []strategy `json:"items"`
		Next  string     `json:"next_cursor"`
	}](t, pageRaw)
	if len(page.Items) != 1 || page.Next == "" {
		t.Fatal("strategy list not paginated")
	}
	a.call("GET", "/strategies?cursor="+url.QueryEscape(page.Next), alice, nil, 200)
	for _, test := range []struct {
		source string
		valid  bool
	}{
		{"def run(data):\n    return data\n", true}, {"", true}, {"raise RuntimeError('never executed')\nwhile True: pass", true}, {"def broken(:\n pass", false}, {"return 1", false}, {"x = '😀'; )", false},
	} {
		d := decodeWorkspace[struct {
			Valid       bool `json:"valid"`
			Diagnostics []struct {
				Line      int    `json:"line"`
				Column    int    `json:"column"`
				EndLine   int    `json:"end_line"`
				EndColumn int    `json:"end_column"`
				Severity  string `json:"severity"`
				Message   string `json:"message"`
			} `json:"diagnostics"`
		}](t, a.call("POST", "/strategies/lint", alice, map[string]string{"source": test.source, "language": "python"}, 200))
		if d.Valid != test.valid {
			t.Fatal("incorrect Python syntax result")
		}
		if !test.valid && (len(d.Diagnostics) == 0 || d.Diagnostics[0].Line < 1 || d.Diagnostics[0].Column < 1 || d.Diagnostics[0].Severity != "error") {
			t.Fatal("missing Monaco diagnostic")
		}
		if !test.valid && (d.Diagnostics[0].EndLine < d.Diagnostics[0].Line || d.Diagnostics[0].EndColumn < 1) {
			t.Fatal("missing Monaco end position")
		}
		if test.source == "x = '😀'; )" && d.Diagnostics[0].Column != 11 {
			t.Fatal("Monaco columns must count UTF-16 code units")
		}
	}
	a.call("POST", "/strategies/lint", alice, map[string]string{"source": "pass", "language": "rust"}, 400)
	a.call("POST", "/strategies/lint", alice, map[string]string{"source": strings.Repeat("x", 100001)}, 400)
	a.call("DELETE", "/strategies/"+imported.ID, alice, nil, 204)
	a.call("DELETE", path, alice, nil, 204)
	a.call("GET", path, alice, nil, 404)
	a.call("GET", path+"/versions", alice, nil, 404)
}
