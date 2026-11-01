//go:build e2e

package e2e

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
)

// Reuse the two authenticated accounts; never read another service's database.
func exerciseMarketplace(t *testing.T, a, b workspaceAPI, alice, bob string) {
	t.Helper()
	type artifact struct {
		ID        string `json:"id"`
		VersionID string `json:"version_id"`
		Source    string `json:"source"`
		ReadOnly  bool   `json:"read_only"`
	}
	type item struct {
		ID           string  `json:"id"`
		VersionID    string  `json:"version_id"`
		Revision     int64   `json:"revision"`
		InstallCount int     `json:"install_count"`
		ReviewCount  int     `json:"review_count"`
		Average      float64 `json:"average_rating"`
	}
	type installation struct {
		ItemID      string `json:"item_id"`
		CatalogID   string `json:"catalog_id"`
		VersionID   string `json:"version_id"`
		Source      string `json:"source"`
		InstalledAt string `json:"installed_at"`
	}
	input := map[string]any{"name": "Community chart", "language": "python", "source": "raise RuntimeError('never execute published source')", "configuration": map[string]any{"period": 20}}
	widget := decodeWorkspace[artifact](t, a.call("POST", "/widgets/custom", alice, input, 201))
	publish := map[string]any{"kind": "indicator", "resource_id": widget.ID, "version_id": widget.VersionID, "name": "Community indicator", "description": "Searchable shared artifact", "category": "indicators"}
	a.call("POST", "/marketplace/items", "", publish, 401)
	b.call("POST", "/marketplace/items", bob, publish, 404)
	listed := decodeWorkspace[item](t, a.call("POST", "/marketplace/items", alice, publish, 201))
	path := "/marketplace/items/" + listed.ID
	a.call("POST", "/marketplace/items", alice, publish, 409)
	public := a.call("GET", path, "", nil, 200)
	if bytes.Contains(public, []byte(`"source"`)) || bytes.Contains(public, []byte(`"resource_id"`)) {
		t.Fatal("private snapshot leaked in public metadata")
	}
	for _, suffix := range []string{"?q=searchable&kind=indicator&category=indicators", "?limit=1"} {
		a.call("GET", "/marketplace/items"+suffix, "", nil, 200)
	}
	categories := decodeWorkspace[struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Next string `json:"next_cursor"`
	}](t, a.call("GET", "/marketplace/categories?limit=1", "", nil, 200))
	if len(categories.Items) != 1 || categories.Next == "" {
		t.Fatal("category pagination")
	}
	a.call("GET", "/marketplace/categories?limit=1&cursor="+url.QueryEscape(categories.Next), "", nil, 200)
	b.call("PATCH", path, bob, map[string]any{"base_revision": listed.Revision, "name": "Stolen"}, 404)
	b.call("DELETE", path, bob, nil, 404)
	b.call("POST", path+"/reviews", bob, map[string]any{"rating": 5}, 403)
	a.call("POST", path+"/reviews", alice, map[string]any{"rating": 5}, 403)
	pin := map[string]string{"version_id": widget.VersionID}
	installed := decodeWorkspace[installation](t, b.call("POST", path+"/install", bob, pin, 201))
	retry := decodeWorkspace[installation](t, b.call("POST", path+"/install", bob, pin, 201))
	if installed.Source != widget.Source || installed.VersionID != widget.VersionID || retry.InstalledAt != installed.InstalledAt {
		t.Fatal("snapshot install/retry changed content")
	}
	b.call("POST", path+"/reviews", bob, map[string]any{"rating": 5, "comment": "Useful"}, 201)
	b.call("POST", path+"/reviews", bob, map[string]any{"rating": 2}, 409)
	a.call("GET", path+"/reviews?limit=1", "", nil, 200)
	metrics := decodeWorkspace[item](t, a.call("GET", path, "", nil, 200))
	if metrics.InstallCount != 1 || metrics.ReviewCount != 1 || metrics.Average != 5 {
		t.Fatal("rating/install metrics incorrect")
	}
	featured := decodeWorkspace[struct {
		Items []item `json:"items"`
	}](t, a.call("GET", "/marketplace/featured", "", nil, 200))
	found := false
	for _, v := range featured.Items {
		found = found || v.ID == listed.ID
	}
	if !found {
		t.Fatal("popular item missing from featured selection")
	}
	catalogue := decodeWorkspace[struct {
		Items []artifact `json:"items"`
		Next  string     `json:"next_cursor"`
	}](t, b.call("GET", "/widgets/installed", bob, nil, 200))
	found = false
	for _, v := range catalogue.Items {
		found = found || v.ID == installed.CatalogID
	}
	if !found {
		t.Fatal("installed indicator absent from widget catalogue")
	}
	// The installation is a real Workspace reference, not just a Marketplace row.
	d := decodeWorkspace[dashboard](t, b.call("POST", "/dashboards", bob, map[string]string{"title": "Community"}, 201))
	dashboardPath := "/dashboards/" + d.ID
	reference := map[string]string{"widget_id": installed.CatalogID, "widget_version_id": installed.VersionID}
	placement := decodeWorkspace[widgetInstance](t, b.call("POST", dashboardPath+"/widgets", bob, reference, 201))
	input["source"] = "def render(data, parameters): return []"
	newWidget := decodeWorkspace[artifact](t, a.call("PUT", "/widgets/custom/"+widget.ID, alice, input, 200))
	listed = decodeWorkspace[item](t, a.call("PATCH", path, alice, map[string]any{"base_revision": listed.Revision, "version_id": newWidget.VersionID}, 200))
	a.call("PATCH", path, alice, map[string]any{"base_revision": 1, "name": "Stale"}, 409)
	still := decodeWorkspace[installation](t, b.call("POST", path+"/install", bob, pin, 201))
	if still.VersionID != widget.VersionID || still.Source != widget.Source {
		t.Fatal("publication upgrade changed subscriber pin")
	}
	a.call("DELETE", path, alice, nil, 204)
	a.call("GET", path, "", nil, 404)
	a.call("GET", path+"/reviews", "", nil, 404)
	b.call("POST", path+"/install", bob, pin, 201)
	a.call("POST", path+"/install", alice, pin, 404)
	b.call("DELETE", path+"/installation", bob, nil, 204)
	b.call("POST", dashboardPath+"/widgets", bob, reference, 404)
	b.call("PATCH", dashboardPath+"/widgets/"+placement.InstanceID, bob, map[string]any{"configuration": map[string]any{"period": 30}}, 200)
	b.call("DELETE", dashboardPath, bob, nil, 204)
	a.call("DELETE", "/widgets/custom/"+widget.ID, alice, nil, 204)
	// Strategies install as private read-only snapshots, not editable author data.
	strategy := decodeWorkspace[artifact](t, a.call("POST", "/strategies", alice, map[string]any{"name": "Shared strategy", "source": "raise RuntimeError('never execute')"}, 201))
	strategyInput := map[string]any{"kind": "strategy", "resource_id": strategy.ID, "version_id": strategy.VersionID, "name": "Unlisted strategy", "category": "strategies", "visibility": "unlisted"}
	b.call("POST", "/marketplace/items", bob, strategyInput, 404)
	strategyItem := decodeWorkspace[item](t, a.call("POST", "/marketplace/items", alice, strategyInput, 201))
	strategyPath := "/marketplace/items/" + strategyItem.ID
	hidden := decodeWorkspace[struct {
		Items []item `json:"items"`
	}](t, a.call("GET", "/marketplace/items?q=Unlisted%20strategy", "", nil, 200))
	if len(hidden.Items) != 0 {
		t.Fatal("unlisted listing discovered")
	}
	a.call("GET", strategyPath, "", nil, 200)
	strategyInstall := decodeWorkspace[installation](t, b.call("POST", strategyPath+"/install", bob, map[string]string{"version_id": strategy.VersionID}, 201))
	readPath := "/strategies/" + strategyInstall.CatalogID
	read := decodeWorkspace[artifact](t, b.call("GET", readPath, bob, nil, 200))
	if !read.ReadOnly || read.Source != strategy.Source {
		t.Fatal("installed strategy not pinned/read-only")
	}
	a.call("GET", readPath, alice, nil, 404)
	b.call("GET", readPath+"/versions", bob, nil, 200)
	b.call("PUT", readPath, bob, map[string]any{"name": "Overwrite", "source": "pass", "base_version_id": strategy.VersionID}, 404)
	// Exercise merged pagination across private and Marketplace namespaces.
	privateIDs := []string{}
	for n := 0; n < 3; n++ {
		local := decodeWorkspace[artifact](t, b.call("POST", "/strategies", bob, map[string]string{"name": "Private", "source": "pass"}, 201))
		privateIDs = append(privateIDs, local.ID)
	}
	cursor := ""
	seen := map[string]bool{}
	for n := 0; n < 20; n++ {
		p := decodeWorkspace[struct {
			Items []artifact `json:"items"`
			Next  string     `json:"next_cursor"`
		}](t, b.call("GET", "/strategies?limit=1&cursor="+url.QueryEscape(cursor), bob, nil, 200))
		for _, v := range p.Items {
			if seen[v.ID] {
				t.Fatal("merged pagination repeated item")
			}
			seen[v.ID] = true
		}
		if p.Next == "" {
			break
		}
		cursor = p.Next
	}
	if !seen[strategyInstall.CatalogID] {
		t.Fatal("installed strategy missing from paginated workspace")
	}
	if len(seen) != 4 {
		t.Fatal("mixed pagination lost private/installed entries")
	}
	for _, id := range privateIDs {
		b.call("DELETE", "/strategies/"+id, bob, nil, 204)
	}
	a.call("DELETE", strategyPath, alice, nil, 204)
	b.call("GET", readPath, bob, nil, 200)
	b.call("DELETE", strategyPath+"/installation", bob, nil, 204)
	b.call("GET", readPath, bob, nil, 404)
	a.call("DELETE", "/strategies/"+strategy.ID, alice, nil, 204)
	a.call("POST", "/catalogue/snapshot", alice, map[string]string{}, 404)
	for _, query := range []string{"?limit=0", "?kind=unknown", "?q=" + strings.Repeat("x", 201)} {
		a.call("GET", "/marketplace/items"+query, "", nil, 400)
	}
}
