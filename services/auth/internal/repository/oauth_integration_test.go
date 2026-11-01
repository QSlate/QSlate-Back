//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	authhttp "qslate/services/auth/internal/http"
	"qslate/services/auth/internal/oauth"
	"qslate/services/auth/internal/platform/token"
	"qslate/services/auth/internal/secretbox"
	"qslate/services/auth/internal/service"
	"qslate/services/auth/migrations"
)

func TestConfigurableOAuthCodePKCEAndAccountSeparation(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	keyPath := signingKey(t)
	signer, err := token.NewSigner(keyPath, "auth-test", "api-test", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	mail := &capturedMail{tokens: map[string]string{}}
	svc, err := service.New(repo, signer, mail, service.Config{AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, BcryptCost: 4})
	if err != nil {
		t.Fatal(err)
	}
	var expectedChallenge string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			if r.Method != "POST" || r.ParseForm() != nil {
				w.WriteHeader(400)
				return
			}
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != expectedChallenge || r.Form.Get("client_id") != "test-client" || r.Form.Get("client_secret") != "test-secret" || r.Form.Get("code") != "one-time-code" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "provider-token", "token_type": "Bearer"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer provider-token" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"identity": map[string]any{"id": 12345, "email": "oauth@example.com", "verified": true, "name": "OAuth User"}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	t.Setenv("TEST_PROVIDER_ID", "test-client")
	t.Setenv("TEST_PROVIDER_SECRET", "test-secret")
	configuration := map[string]any{"providers": []any{map[string]any{"id": "custom-provider", "display_name": "Configured Provider", "enabled": true, "authorize_url": provider.URL + "/authorize", "token_url": provider.URL + "/token", "userinfo_url": provider.URL + "/userinfo", "redirect_url": "http://localhost:8080/qslate/v1/auth/oauth/custom-provider/callback", "client_id_env": "TEST_PROVIDER_ID", "client_secret_env": "TEST_PROVIDER_SECRET", "token_auth_method": "client_secret_post", "scopes": []string{"profile", "email"}, "claims": map[string]string{"subject": "identity.id", "email": "identity.email", "email_verified": "identity.verified", "name": "identity.name"}, "allow_http_loopback": true}}}
	data, _ := json.Marshal(configuration)
	configPath := filepath.Join(t.TempDir(), "providers.json")
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	providers, err := oauth.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := authhttp.Config{AllowedOrigins: []string{"http://localhost:3000"}}
	oauthHandler := oauth.New(providers, repo, box, svc, cfg, "http://localhost:3000")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(http.StripPrefix("/qslate/v1", authhttp.New(svc, signer, db.PingContext, logger, cfg, oauthHandler.Routes)))
	defer server.Close()
	client := newClient(t, server.URL)
	client.call("GET", "/auth/providers", "", nil, 200)
	start := func() (string, string) {
		t.Helper()
		response, err := client.client.Get(server.URL + "/qslate/v1/auth/oauth/custom-provider/authorize")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 302 {
			t.Fatal("authorization did not redirect")
		}
		target, err := url.Parse(response.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if target.Host == "" || target.Query().Get("code_challenge_method") != "S256" || target.Query().Get("client_secret") != "" {
			t.Fatal("invalid authorization URL")
		}
		expectedChallenge = target.Query().Get("code_challenge")
		return target.Query().Get("state"), response.Cookies()[0].Value
	}
	state, stateCookie := start()
	response, err := client.client.Get(server.URL + "/qslate/v1/auth/oauth/custom-provider/callback?state=" + state + "&code=one-time-code")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 || response.Header.Get("Location") != "http://localhost:3000/auth/callback?status=success" {
		t.Fatalf("OAuth callback failed: %d", response.StatusCode)
	}
	login := sessionResponse(t, client.call("POST", "/auth/refresh", "", nil, 200))
	if login.User.Email != "oauth@example.com" || !login.User.EmailVerified {
		t.Fatal("provider identity mapping not applied")
	}
	client.call("GET", "/users/me", login.AccessToken, nil, 200)
	// Single-use callback even if a stale browser cookie is replayed.
	replayURL, _ := url.Parse(server.URL + "/qslate/v1/auth/oauth/custom-provider/callback")
	client.client.Jar.SetCookies(replayURL, []*http.Cookie{{Name: "qslate_oauth_state", Value: stateCookie, Path: "/qslate/v1/auth/oauth"}})
	client.call("GET", "/auth/oauth/custom-provider/callback?state="+state+"&code=one-time-code", "", nil, 400)
	// A second authorization resolves the same external identity, not a second account.
	state, _ = start()
	response, err = client.client.Get(server.URL + "/qslate/v1/auth/oauth/custom-provider/callback?state=" + state + "&code=one-time-code")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatal("returning external user rejected")
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM users WHERE email='oauth@example.com'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate external account")
	}
	// Different provider + same email must not take over the existing account.
	providers[0].ID = "another-provider"
	otherHandler := oauth.New(providers, repo, box, svc, cfg, "http://localhost:3000")
	otherMux := http.NewServeMux()
	otherHandler.Routes(otherMux)
	other := httptest.NewServer(http.StripPrefix("/qslate/v1", otherMux))
	defer other.Close()
	attacker := newClient(t, other.URL)
	response, err = attacker.client.Get(other.URL + "/qslate/v1/auth/oauth/another-provider/authorize")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	u, _ := url.Parse(response.Header.Get("Location"))
	state = u.Query().Get("state")
	expectedChallenge = u.Query().Get("code_challenge")
	attacker.call("GET", "/auth/oauth/another-provider/callback?state="+state+"&code=one-time-code", "", nil, 409)
	client.call("GET", "/auth/oauth/missing/authorize", "", nil, 404)
}
