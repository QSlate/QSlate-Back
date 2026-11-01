//go:build integration

package repository

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	authhttp "qslate/services/auth/internal/http"
	"qslate/services/auth/internal/platform/token"
	"qslate/services/auth/internal/service"
	"qslate/services/auth/migrations"
)

type capturedMail struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (m *capturedMail) Seal(email, kind, raw string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[email+":"+kind] = raw
	return []byte("test-ciphertext"), nil
}
func (m *capturedMail) get(email, kind string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens[email+":"+kind]
}

func signingKey(t *testing.T) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "auth.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type apiClient struct {
	t      *testing.T
	client *http.Client
	base   string
}

func newClient(t *testing.T, base string) *apiClient {
	jar, _ := cookiejar.New(nil)
	return &apiClient{t: t, base: base, client: &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *apiClient) call(method, path, access string, input any, want int) []byte {
	c.t.Helper()
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			c.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, c.base+"/qslate/v1"+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("X-User-ID", "999")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	response, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		c.t.Fatal(err)
	}
	if response.StatusCode != want {
		c.t.Fatalf("%s %s returned %d want %d (%s)", method, path, response.StatusCode, want, data)
	}
	if bytes.Contains(data, []byte("password_hash")) || bytes.Contains(data, []byte("refresh_token")) {
		c.t.Fatal("secret field leaked")
	}
	return data
}
func sessionResponse(t *testing.T, data []byte) service.Session {
	t.Helper()
	var result service.Session
	if err := json.Unmarshal(data, &result); err != nil || result.AccessToken == "" {
		t.Fatal("invalid session response")
	}
	return result
}

func TestAccountHTTPPersistenceLifecycle(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	signer, err := token.NewSigner(signingKey(t), "auth-test", "api-test", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	mail := &capturedMail{tokens: map[string]string{}}
	svc, err := service.New(repo, signer, mail, service.Config{BcryptCost: 4, AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(http.StripPrefix("/qslate/v1", authhttp.New(svc, signer, db.PingContext, logger, authhttp.Config{AllowedOrigins: []string{"http://localhost:3000"}})))
	defer server.Close()
	a := newClient(t, server.URL)
	b := newClient(t, server.URL)
	password := "correct horse battery staple"
	email := "alice@example.com"
	registered := sessionResponse(t, a.call("POST", "/auth/register", "", map[string]string{"email": email, "username": "alice", "password": password}, 201))
	if registered.User.EmailVerified {
		t.Fatal("new password account marked verified")
	}
	cookieURL, _ := url.Parse(server.URL + "/qslate/v1/auth/refresh")
	cookies := a.client.Jar.Cookies(cookieURL)
	if len(cookies) != 1 || len(cookies[0].Value) != 43 {
		t.Fatal("refresh cookie missing")
	}
	var rawCount int
	if err = db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE hash=$1`, cookies[0].Value).Scan(&rawCount); err != nil || rawCount != 0 {
		t.Fatal("raw refresh token stored")
	}
	a.call("GET", "/users/me", registered.AccessToken, nil, 200)
	a.call("PATCH", "/users/me", registered.AccessToken, map[string]string{"display_name": "Alice", "username": "Alice_New"}, 200)
	a.call("GET", "/users/me/preferences", registered.AccessToken, nil, 200)
	data := a.call("PATCH", "/users/me/preferences", registered.AccessToken, map[string]any{"theme": "light", "colorblind_mode": true}, 200)
	if !bytes.Contains(data, []byte(`"colorblind_mode":true`)) {
		t.Fatal("preferences not updated")
	}
	a.call("PATCH", "/users/me/preferences", registered.AccessToken, map[string]any{"colorblind_mode": false}, 200)
	a.call("PATCH", "/users/me/preferences", registered.AccessToken, map[string]string{"theme": "invalid"}, 400)
	a.call("PATCH", "/users/me", registered.AccessToken, map[string]string{"email": "other@example.com"}, 400)
	other := sessionResponse(t, b.call("POST", "/auth/register", "", map[string]string{"email": "bob@example.com", "username": "bob", "password": password}, 201))
	a.call("PATCH", "/users/me", registered.AccessToken, map[string]string{"username": "bob"}, 409)
	var created service.NewAPIKey
	a.call("POST", "/users/me/api-keys", registered.AccessToken, map[string]any{"name": "invalid expiry", "expires_in_days": 0}, 400)
	data = a.call("POST", "/users/me/api-keys", registered.AccessToken, map[string]any{"name": "local CLI", "scopes": []string{"runner:results:write"}}, 201)
	if err = json.Unmarshal(data, &created); err != nil || !strings.HasPrefix(created.Secret, "qsl_") {
		t.Fatal("API key not returned once")
	}
	data = a.call("GET", "/users/me/api-keys", registered.AccessToken, nil, 200)
	if bytes.Contains(data, []byte(created.Secret)) {
		t.Fatal("API secret repeated in list")
	}
	b.call("DELETE", "/users/me/api-keys/"+created.Key.ID, other.AccessToken, nil, 404)
	a.call("DELETE", "/users/me/api-keys/"+created.Key.ID, registered.AccessToken, nil, 204)
	a.call("DELETE", "/users/me/api-keys/"+created.Key.ID, registered.AccessToken, nil, 204)
	a.call("GET", "/users/me", created.Secret, nil, 401)
	a.call("POST", "/auth/verify-email", "", map[string]string{"token": mail.get(email, "verify_email")}, 204)
	a.call("POST", "/auth/verify-email", "", map[string]string{"token": mail.get(email, "verify_email")}, 400)
	profile := a.call("GET", "/users/me", registered.AccessToken, nil, 200)
	if !bytes.Contains(profile, []byte(`"email_verified":true`)) {
		t.Fatal("verification not persisted")
	}
	oldCookie := a.client.Jar.Cookies(cookieURL)[0].Value
	rotated := sessionResponse(t, a.call("POST", "/auth/refresh", "", nil, 200))
	if a.client.Jar.Cookies(cookieURL)[0].Value == oldCookie {
		t.Fatal("refresh did not rotate")
	}
	attacker := newClient(t, server.URL)
	attacker.client.Jar.SetCookies(cookieURL, []*http.Cookie{{Name: authhttp.RefreshCookie, Value: oldCookie, Path: authhttp.CookiePath}})
	attacker.call("POST", "/auth/refresh", "", nil, 401)
	a.call("GET", "/users/me", rotated.AccessToken, nil, 401)
	a.call("POST", "/auth/refresh", "", nil, 401)
	login := sessionResponse(t, a.call("POST", "/auth/login", "", map[string]string{"identifier": "alice_new", "password": password}, 200))
	a.call("POST", "/auth/logout", "", nil, 204)
	a.call("GET", "/users/me", login.AccessToken, nil, 401)
	a.call("POST", "/auth/logout", "", nil, 204)
	login = sessionResponse(t, a.call("POST", "/auth/login", "", map[string]string{"identifier": email, "password": password}, 200))
	known := a.call("POST", "/auth/forgot-password", "", map[string]string{"email": email}, 202)
	unknown := a.call("POST", "/auth/forgot-password", "", map[string]string{"email": "absent@example.com"}, 202)
	if !bytes.Equal(known, unknown) {
		t.Fatal("forgot-password reveals account existence")
	}
	reset := mail.get(email, "reset_password")
	newPassword := "new correct horse battery"
	a.call("POST", "/auth/reset-password", "", map[string]string{"token": reset, "password": newPassword}, 204)
	a.call("POST", "/auth/reset-password", "", map[string]string{"token": reset, "password": newPassword}, 400)
	a.call("GET", "/users/me", login.AccessToken, nil, 401)
	a.call("POST", "/auth/login", "", map[string]string{"identifier": email, "password": password}, 401)
	a.call("POST", "/auth/login", "", map[string]string{"identifier": email, "password": newPassword}, 200)
	// Invalid origin cannot refresh even when its session cookie is valid.
	request, _ := http.NewRequest("POST", server.URL+"/qslate/v1/auth/refresh", nil)
	request.Header.Set("Origin", "https://evil.example")
	response, err := a.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin refresh accepted")
	}
}
