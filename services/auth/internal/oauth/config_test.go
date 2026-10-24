package oauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func configuredProvider() Provider {
	return Provider{ID: "custom", Enabled: true, DisplayName: "Custom",
		AuthorizeURL: "https://identity.example/authorize", TokenURL: "https://identity.example/token",
		UserinfoURL: "https://identity.example/userinfo", RedirectURL: "https://api.example/qslate/v1/auth/oauth/custom/callback",
		ClientIDEnv: "QSLATE_TEST_CLIENT_ID", ClientSecretEnv: "QSLATE_TEST_CLIENT_SECRET", TokenAuth: "client_secret_basic",
		Scopes: []string{"email"}, Claims: Claims{Subject: "id", Email: "email", Verified: "email_verified"}}
}

func TestProviderValidation(t *testing.T) {
	t.Setenv("QSLATE_TEST_CLIENT_ID", "client")
	t.Setenv("QSLATE_TEST_CLIENT_SECRET", "secret")
	for _, tc := range []struct {
		name   string
		change func(*Provider)
		valid  bool
	}{
		{"valid", func(*Provider) {}, true},
		{"remote HTTP rejected", func(p *Provider) { p.TokenURL = "http://identity.example/token"; p.AllowHTTP = true }, false},
		{"loopback requires opt in", func(p *Provider) { p.TokenURL = "http://127.0.0.1/token" }, false},
		{"explicit loopback", func(p *Provider) { p.TokenURL = "http://127.0.0.1/token"; p.AllowHTTP = true }, true},
		{"redirect must bind provider", func(p *Provider) { p.RedirectURL = "https://api.example/qslate/v1/auth/oauth/other/callback" }, false},
		{"redirect HTTP rejected", func(p *Provider) { p.RedirectURL = "http://api.example/qslate/v1/auth/oauth/custom/callback" }, false},
		{"URL credentials rejected", func(p *Provider) { p.TokenURL = "https://user:secret@identity.example/token" }, false},
		{"URL query rejected", func(p *Provider) { p.AuthorizeURL += "?client_secret=secret" }, false},
		{"verified claim required", func(p *Provider) { p.Claims.Verified = "" }, false},
		{"missing credential", func(p *Provider) { p.ClientSecretEnv = "QSLATE_TEST_MISSING_SECRET" }, false},
		{"public client", func(p *Provider) { p.TokenAuth = "none"; p.ClientSecretEnv = "" }, true},
		{"unknown token auth", func(p *Provider) { p.TokenAuth = "magic" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := configuredProvider()
			tc.change(&provider)
			if err := validate(&provider); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestLoadStrictAndDisabledProviders(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"disabled without credentials", `{"providers":[{"id":"custom","enabled":false}]}`, true},
		{"unknown field", `{"providers":[],"secret":"wrong"}`, false},
		{"duplicate provider", `{"providers":[{"id":"custom"},{"id":"custom"}]}`, false},
		{"invalid provider id", `{"providers":[{"id":"../escape"}]}`, false},
		{"trailing JSON", `{"providers":[]} {}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "providers.json")
			if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			providers, err := Load(path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.valid && len(providers) != 0 {
				t.Fatal("disabled provider exposed")
			}
		})
	}
}

func TestClaimMappingPreservesLargeNumericSubject(t *testing.T) {
	data := map[string]any{"identity": map[string]any{"id": json.Number("9007199254740993")}}
	if claim(data, "identity.id") != json.Number("9007199254740993") || claim(data, "identity.missing") != nil {
		t.Fatal("nested claims incorrectly resolved")
	}
}
