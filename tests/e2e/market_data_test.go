//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type importedDataset struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
	Status    string `json:"status"`
	Symbol    string `json:"symbol"`
	RowCount  int    `json:"row_count"`
	SHA256    string `json:"sha256"`
}

func exerciseMarketData(t *testing.T, a, b workspaceAPI, alice, bob string) {
	t.Helper()
	a.call("GET", "/data/custom", "", nil, 401)
	a.call("GET", "/data/symbols", "", nil, 401)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("name", "Synthetic E2E candles")
	f, e := form.CreateFormFile("file", "synthetic.csv")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.WriteString(f, "date,o,h,l,c,v\n")
	for i := 0; i < 40; i++ {
		_, _ = fmt.Fprintf(f, "%s,%d,%d,%d,%d,100\n", time.Date(2024, 1, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339), 100+i, 102+i, 99+i, 101+i)
	}
	_ = form.Close()
	req, _ := http.NewRequest("POST", a.base+"/qslate/v1/data/custom/upload", &body)
	req.Header.Set("Authorization", "Bearer "+alice)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Origin", "http://localhost:3000")
	resp, e := a.client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, raw)
	}
	d := decodeWorkspace[importedDataset](t, raw)
	if d.Status != "uploaded" || d.RowCount != 40 || d.Symbol != "custom:"+d.ID {
		t.Fatal(d)
	}
	path := "/data/custom/" + d.ID
	b.call("GET", path, bob, nil, 404)
	b.call("DELETE", path, bob, nil, 404)
	mapping := map[string]any{"base_version_id": d.VersionID, "timestamp_column": "date", "timezone": "UTC", "timestamp_unit": "iso8601", "columns": map[string]string{"open": "o", "high": "h", "low": "l", "close": "c", "volume": "v"}}
	b.call("POST", path+"/mapping", bob, mapping, 404)
	mapped := decodeWorkspace[importedDataset](t, a.call("POST", path+"/mapping", alice, mapping, 200))
	if mapped.VersionID == d.VersionID || mapped.Status != "mapped" {
		t.Fatal(mapped)
	}
	a.call("POST", path+"/mapping", alice, mapping, 409)
	validate := func(access, key string, status int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest("POST", a.base+"/qslate/v1"+path+"/validate", nil)
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Origin", "http://localhost:3000")
		resp, e := a.client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status {
			t.Fatalf("validate: %d %s", resp.StatusCode, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	validate(bob, "foreign-validation-0001", 404)
	job := validate(alice, "dataset-validation-0001", 202)
	replay := validate(alice, "dataset-validation-0001", 202)
	if job["job_id"] != replay["job_id"] {
		t.Fatal("validation not idempotent")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		d = decodeWorkspace[importedDataset](t, a.call("GET", path, alice, nil, 200))
		if d.Status == "ready" {
			break
		}
		if d.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("validation did not complete: %+v", d)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if d.RowCount != 40 || len(d.SHA256) != 64 {
		t.Fatal(d)
	}
	history := "/data/history?symbol=" + url.QueryEscape(d.Symbol) + "&start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&resolution=raw&limit=1"
	page := decodeWorkspace[struct {
		Items []any  `json:"items"`
		Next  string `json:"next_cursor"`
	}](t, a.call("GET", history, alice, nil, 200))
	if len(page.Items) != 1 || page.Next == "" {
		t.Fatal("history not paginated")
	}
	a.call("GET", history+"&cursor="+url.QueryEscape(page.Next), alice, nil, 200)
	b.call("GET", history, bob, nil, 404)
	aggregated := decodeWorkspace[struct {
		Items []any `json:"items"`
	}](t, a.call("GET", strings.Replace(history, "resolution=raw", "resolution=1h", 1), alice, nil, 200))
	if len(aggregated.Items) != 1 {
		t.Fatal("aggregation missing")
	}
	a.call("GET", "/data/symbols?q=Synthetic", alice, nil, 200)
	b.call("GET", "/data/custom", bob, nil, 200)
	mapping["base_version_id"] = d.VersionID
	a.call("POST", path+"/mapping", alice, mapping, 200)
	a.call("GET", history+"&version_id="+d.VersionID, alice, nil, 200)
	a.call("POST", "/catalogue/dataset-snapshot", alice, map[string]string{"dataset_id": d.ID, "version_id": d.VersionID}, 404)
	a.call("DELETE", path, alice, nil, 204)
	a.call("GET", path, alice, nil, 404)
	a.call("GET", history, alice, nil, 404)
}
