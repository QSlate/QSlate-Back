//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

type workspaceAPI struct {
	t      *testing.T
	base   string
	client *http.Client
}

func newWorkspaceAPI(t *testing.T) workspaceAPI {
	t.Helper()
	base := strings.TrimRight(os.Getenv("API_BASE_URL"), "/")
	if base == "" {
		t.Fatal("API_BASE_URL required (running gateway)")
	}
	jar, _ := cookiejar.New(nil)
	return workspaceAPI{t, base, &http.Client{Timeout: 15 * time.Second, Jar: jar}}
}
func (a workspaceAPI) call(method, path, access string, input any, status int) []byte {
	a.t.Helper()
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			a.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, a.base+"/qslate/v1"+path, bytes.NewReader(body))
	if err != nil {
		a.t.Fatal(err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("X-User-ID", "1")
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		a.t.Fatalf("%s %s: got %d, want %d", method, path, resp.StatusCode, status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		a.t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"owner_id"`)) {
		a.t.Fatal("internal owner field leaked")
	}
	return data
}

type widgetInstance struct {
	InstanceID    string          `json:"instance_id"`
	WidgetID      string          `json:"widget_id"`
	VersionID     string          `json:"widget_version_id"`
	Configuration json.RawMessage `json:"configuration"`
}
type layoutPosition struct {
	InstanceID string `json:"instance_id"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}
type dashboard struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Revision    int64            `json:"revision"`
	Widgets     []widgetInstance `json:"widgets"`
	Layout      []layoutPosition `json:"layout"`
}

func decodeWorkspace[T any](t *testing.T, data []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWorkspaceThroughGateway(t *testing.T) {
	a, b := newWorkspaceAPI(t), newWorkspaceAPI(t)
	register := func(api workspaceAPI, prefix string) (string, string) {
		name := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
		email := name + "@example.com"
		data := api.call("POST", "/auth/register", "", map[string]string{"email": email, "username": name, "password": "correct horse battery staple"}, 201)
		session := decodeWorkspace[struct {
			AccessToken string `json:"access_token"`
		}](t, data)
		if session.AccessToken == "" {
			t.Fatal("registration access token missing")
		}
		return session.AccessToken, email
	}
	alice, email := register(a, "wsa")
	bob, _ := register(b, "wsb")
	type definition struct {
		ID        string `json:"id"`
		VersionID string `json:"version_id"`
		Source    string `json:"source"`
	}
	widgetInput := map[string]any{"name": "Price chart", "language": "python", "source": "def render(data, parameters):\n    return data\n", "configuration": map[string]any{}}
	a.call("GET", "/widgets/installed", "", nil, 401)
	first := decodeWorkspace[definition](t, a.call("POST", "/widgets/custom", alice, widgetInput, 201))
	secondDefinition := decodeWorkspace[definition](t, a.call("POST", "/widgets/custom", alice, map[string]string{"name": "Orders", "language": "rust", "source": "fn main() {}"}, 201))
	definitionPath := "/widgets/custom/" + first.ID
	b.call("GET", definitionPath, bob, nil, 404)
	b.call("PUT", definitionPath, bob, widgetInput, 404)
	b.call("DELETE", definitionPath, bob, nil, 404)
	b.call("POST", definitionPath+"/preview", bob, map[string]string{"dataset_id": "sample"}, 404)
	privateList := decodeWorkspace[struct {
		Items []definition `json:"items"`
	}](t, b.call("GET", "/widgets/custom", bob, nil, 200))
	if len(privateList.Items) != 0 {
		t.Fatal("widget definitions leaked between owners")
	}
	a.call("GET", definitionPath, alice, nil, 200)
	a.call("GET", "/widgets/installed?limit=1", alice, nil, 200)
	a.call("GET", "/widgets/custom?limit=1", alice, nil, 200)
	a.call("POST", definitionPath+"/preview", alice, map[string]string{"dataset_id": "sample"}, 503) // no sandbox controller configured; never simulated execution
	widgetInput["source"] = "def render(data, parameters):\n    return []\n"
	newVersion := decodeWorkspace[definition](t, a.call("PUT", definitionPath, alice, widgetInput, 200))
	if newVersion.VersionID == first.VersionID {
		t.Fatal("widget update overwrote its immutable version")
	}
	a.call("GET", "/dashboards", "", nil, 401)
	a.call("GET", "/dashboards", "forged", nil, 401)
	a.call("POST", "/dashboards", alice, map[string]any{"title": "Lab", "owner_id": 999}, 400)
	d := decodeWorkspace[dashboard](t, a.call("POST", "/dashboards", alice, map[string]any{"title": "Lab", "description": "Research", "tags": []string{"Forex"}}, 201))
	path := "/dashboards/" + d.ID
	if len(d.ID) != 43 || d.Revision != 1 || d.Widgets == nil || d.Layout == nil {
		t.Fatal("invalid empty dashboard")
	}
	other := decodeWorkspace[dashboard](t, b.call("POST", "/dashboards", bob, map[string]string{"title": "Bob"}, 201))
	page := decodeWorkspace[struct {
		Items []dashboard `json:"items"`
	}](t, a.call("GET", "/dashboards?limit=1", alice, nil, 200))
	if len(page.Items) != 1 || page.Items[0].ID != d.ID {
		t.Fatal("list leaked another user's dashboards")
	}
	d = decodeWorkspace[dashboard](t, a.call("PUT", path, alice, map[string]string{"title": "Updated Lab"}, 200))
	if d.Revision != 2 || d.Description != "" {
		t.Fatal("PUT must replace metadata")
	}
	a.call("POST", path+"/widgets", alice, map[string]string{"widget_id": first.ID, "widget_version_id": secondDefinition.VersionID}, 404)
	b.call("POST", "/dashboards/"+other.ID+"/widgets", bob, map[string]string{"widget_id": first.ID, "widget_version_id": first.VersionID}, 404)
	w := decodeWorkspace[widgetInstance](t, a.call("POST", path+"/widgets", alice, map[string]any{"widget_id": first.ID, "widget_version_id": first.VersionID, "configuration": map[string]any{"symbol": "EURUSD"}}, 201))
	a.call("POST", path+"/widgets", alice, map[string]string{"widget_id": secondDefinition.ID, "widget_version_id": secondDefinition.VersionID}, 201)
	d = decodeWorkspace[dashboard](t, a.call("GET", path, alice, nil, 200))
	if d.Revision != 4 || len(d.Widgets) != 2 || len(d.Layout) != 2 {
		t.Fatal("widget insertion not durable")
	}
	for _, action := range []struct {
		method, suffix string
		input          any
	}{
		{"GET", "", nil}, {"PUT", "", map[string]string{"title": "stolen"}}, {"DELETE", "", nil}, {"POST", "/duplicate", nil},
		{"PUT", "/layout", map[string]any{"revision": d.Revision, "items": d.Layout}}, {"POST", "/layout/reset", nil},
		{"POST", "/widgets", map[string]string{"widget_id": "chart", "widget_version_id": "v1"}},
		{"PATCH", "/widgets/" + w.InstanceID, map[string]any{"configuration": map[string]any{}}}, {"DELETE", "/widgets/" + w.InstanceID, nil},
	} {
		b.call(action.method, path+action.suffix, bob, action.input, 404)
	}
	d.Layout[0].Y = 10
	d = decodeWorkspace[dashboard](t, a.call("PUT", path+"/layout", alice, map[string]any{"revision": d.Revision, "items": d.Layout}, 200))
	a.call("PUT", path+"/layout", alice, map[string]any{"revision": d.Revision - 1, "items": d.Layout}, 409)
	bad := append([]layoutPosition{}, d.Layout...)
	bad[0].X = -1
	a.call("PUT", path+"/layout", alice, map[string]any{"revision": d.Revision, "items": bad}, 400)
	stored := decodeWorkspace[dashboard](t, a.call("GET", path, alice, nil, 200))
	if stored.Revision != d.Revision || stored.Layout[0].Y != 10 {
		t.Fatal("rejected grid modified state")
	}
	patched := decodeWorkspace[widgetInstance](t, a.call("PATCH", path+"/widgets/"+w.InstanceID, alice, map[string]any{"configuration": map[string]any{"symbol": "BTCUSD"}}, 200))
	if patched.VersionID != first.VersionID || !bytes.Contains(patched.Configuration, []byte("BTCUSD")) {
		t.Fatal("widget patch failed")
	}
	copy := decodeWorkspace[dashboard](t, a.call("POST", path+"/duplicate", alice, nil, 201))
	if copy.ID == d.ID || copy.Revision != 1 || len(copy.Widgets) != 2 || copy.Widgets[0].InstanceID == w.InstanceID || copy.Layout[0].InstanceID != copy.Widgets[0].InstanceID || copy.Layout[0].Y != 10 {
		t.Fatal("duplicate did not clone full snapshot with fresh IDs")
	}
	copyPath := "/dashboards/" + copy.ID
	a.call("DELETE", copyPath+"/widgets/"+w.InstanceID, alice, nil, 404)
	a.call("DELETE", copyPath+"/widgets/"+copy.Widgets[0].InstanceID, alice, nil, 204)
	copy = decodeWorkspace[dashboard](t, a.call("POST", copyPath+"/layout/reset", alice, nil, 200))
	if copy.Revision != 3 || len(copy.Widgets) != 1 || len(copy.Layout) != 1 || copy.Layout[0].X != 0 || copy.Layout[0].Y != 0 || copy.Layout[0].Width != 6 {
		t.Fatal("reset or atomic widget deletion failed")
	}
	a.call("DELETE", copyPath, alice, nil, 204)
	a.call("GET", copyPath, alice, nil, 404)
	a.call("DELETE", definitionPath, alice, nil, 204)
	a.call("GET", definitionPath, alice, nil, 404)
	a.call("POST", path+"/widgets", alice, map[string]string{"widget_id": first.ID, "widget_version_id": first.VersionID}, 404)
	a.call("PATCH", path+"/widgets/"+w.InstanceID, alice, map[string]any{"configuration": map[string]any{}}, 200) // retained version on an existing instance
	b.call("POST", "/auth/logout", "", nil, 204)
	b.call("GET", "/dashboards/"+other.ID, bob, nil, 401)
	b.call("GET", "/widgets/custom", bob, nil, 401)
	a.call("GET", path, alice, nil, 200)
	a.call("POST", "/auth/forgot-password", "", map[string]string{"email": email}, 202)
	reset := resetTokenFromMailpit(t, email)
	a.call("POST", "/auth/reset-password", "", map[string]string{"token": reset, "password": "a new correct horse battery staple"}, 204)
	a.call("GET", path, alice, nil, 401)
	a.call("GET", "/widgets/custom", alice, nil, 401)
	a.call("POST", "/auth/refresh", "", nil, 401)
}

// Use the development inbox API, not Auth SQL or test-only production endpoints.
func resetTokenFromMailpit(t *testing.T, email string) string {
	t.Helper()
	base := strings.TrimRight(os.Getenv("MAILPIT_URL"), "/")
	if base == "" {
		base = "http://localhost:8025"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	get := func(path string, out any) bool {
		resp, err := client.Get(base + path)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == 200 && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out) == nil
	}
	pattern := regexp.MustCompile(`/reset-password\?token=([a-zA-Z0-9_-]{43})`)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var page struct {
			Messages []struct {
				ID      string
				Subject string
				To      []struct{ Address string }
			} `json:"messages"`
		}
		if get("/api/v1/messages", &page) {
			for _, message := range page.Messages {
				if message.Subject != "Reset your QSlate password" {
					continue
				}
				for _, to := range message.To {
					if to.Address == email {
						var detail struct{ Text string }
						if get("/api/v1/message/"+message.ID, &detail) {
							if match := pattern.FindStringSubmatch(detail.Text); len(match) == 2 {
								return match[1]
							}
						}
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("reset email not received in development Mailpit inbox (set MAILPIT_URL if using another port)")
	return ""
}
