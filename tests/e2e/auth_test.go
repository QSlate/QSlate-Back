//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	client := &http.Client{Timeout: 15 * time.Second}
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
	call("GET", "/api/auth/me", "", nil, 401)
	call("GET", "/api/auth/me", "forged", nil, 401)
	name := fmt.Sprintf("e2e_%d", time.Now().UnixNano())
	password := "correct horse battery staple"
	input := map[string]string{"email": name + "@example.com", "username": name, "password": password}
	registered := call("POST", "/api/auth/register", "", input, 201)
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
	call("GET", "/api/auth/me", session.AccessToken, nil, 200)
	call("POST", "/api/auth/register", "", input, 409)
	login := call("POST", "/api/auth/login", "", map[string]string{"identifier": name, "password": password}, 200)
	var loggedIn struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(login, &loggedIn); err != nil || loggedIn.AccessToken == "" {
		t.Fatal("missing login token")
	}
	profile := call("GET", "/api/auth/me", loggedIn.AccessToken, nil, 200)
	if !bytes.Contains(profile, []byte(name+"@example.com")) {
		t.Fatal("profile does not belong to the logged-in account")
	}
	call("POST", "/api/auth/login", "", map[string]string{"identifier": name, "password": "wrong-password"}, 401)
	call("POST", "/api/auth/login", "", map[string]string{"identifier": "unknown-user", "password": "wrong-password"}, 401)
	call("GET", "/api/auth/private", "", nil, 404)
}
