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
	"strings"
	"testing"
	"time"
)

func TestAuthThroughGateway(t *testing.T) {
	base := strings.TrimSuffix(os.Getenv("API_BASE_URL"), "/")
	if base == "" {
		t.Fatal("API_BASE_URL required (running gateway)")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	call := func(method, path, access string, input any, want int) []byte {
		t.Helper()
		var body []byte
		if input != nil {
			var err error
			body, err = json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
		}
		request, err := http.NewRequest(method, base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if access != "" {
			request.Header.Set("Authorization", "Bearer "+access)
		}
		request.Header.Set("X-User-ID", "999") // never accepted as identity
		request.Header.Set("Origin", "http://localhost:3000")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s returned %d, want %d", method, path, response.StatusCode, want)
		}
		if response.Header.Get("X-Request-ID") == "" {
			t.Fatal("request ID missing")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("password_hash")) {
			t.Fatal("password hash leaked")
		}
		return data
	}
	call("GET", "/readyz", "", nil, 200)
	call("GET", "/qslate/v1/users/me", "", nil, 401)
	call("GET", "/qslate/v1/users/me", "forged", nil, 401)
	name := fmt.Sprintf("e2e_%d", time.Now().UnixNano())
	password := "correct horse battery staple"
	input := map[string]string{"email": name + "@example.com", "username": name, "password": password}
	registered := call("POST", "/qslate/v1/auth/register", "", input, 201)
	var session struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		User        struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(registered, &session); err != nil || session.User.ID <= 0 || session.AccessToken == "" || session.ExpiresIn != 900 {
		t.Fatal("invalid registration response")
	}
	call("GET", "/qslate/v1/users/me", session.AccessToken, nil, 200)
	call("POST", "/qslate/v1/auth/register", "", input, 409)
	login := call("POST", "/qslate/v1/auth/login", "", map[string]string{"identifier": name, "password": password}, 200)
	var loggedIn struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(login, &loggedIn); err != nil || loggedIn.AccessToken == "" {
		t.Fatal("missing login token")
	}
	profile := call("GET", "/qslate/v1/users/me", loggedIn.AccessToken, nil, 200)
	if !bytes.Contains(profile, []byte(name+"@example.com")) {
		t.Fatal("profile does not belong to the logged-in account")
	}
	call("POST", "/qslate/v1/auth/login", "", map[string]string{"identifier": name, "password": "wrong-password"}, 401)
	call("POST", "/qslate/v1/auth/login", "", map[string]string{"identifier": "unknown-user", "password": "wrong-password"}, 401)
	call("GET", "/qslate/v1/auth/private", "", nil, 404)
	call("PATCH", "/qslate/v1/users/me", loggedIn.AccessToken, map[string]string{"display_name": "E2E Trader"}, 200)
	call("PATCH", "/qslate/v1/users/me/preferences", loggedIn.AccessToken, map[string]any{"theme": "light", "colorblind_mode": true}, 200)
	call("GET", "/qslate/v1/users/me/preferences", loggedIn.AccessToken, nil, 200)
	var key struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(call("POST", "/qslate/v1/users/me/api-keys", loggedIn.AccessToken, map[string]string{"name": "e2e CLI"}, 201), &key); err != nil || key.Key.ID == "" || key.Secret == "" {
		t.Fatal("API key missing")
	}
	call("GET", "/qslate/v1/users/me/api-keys", loggedIn.AccessToken, nil, 200)
	call("DELETE", "/qslate/v1/users/me/api-keys/"+key.Key.ID, loggedIn.AccessToken, nil, 204)
	var rotated struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(call("POST", "/qslate/v1/auth/refresh", "", nil, 200), &rotated); err != nil || rotated.AccessToken == "" {
		t.Fatal("refresh failed")
	}
	call("POST", "/qslate/v1/auth/logout", "", nil, 204)
	call("GET", "/qslate/v1/users/me", rotated.AccessToken, nil, 401)
	call("POST", "/qslate/v1/auth/refresh", "", nil, 401)
	call("GET", "/qslate/v1/auth/providers", "", nil, 200)
}
