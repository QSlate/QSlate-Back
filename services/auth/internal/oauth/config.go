package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
)

type Claims struct {
	Subject  string `json:"subject"`
	Email    string `json:"email"`
	Verified string `json:"email_verified"`
	Name     string `json:"name"`
}
type Provider struct {
	ID                     string            `json:"id"`
	DisplayName            string            `json:"display_name"`
	Enabled                bool              `json:"enabled"`
	AuthorizeURL           string            `json:"authorize_url"`
	TokenURL               string            `json:"token_url"`
	UserinfoURL            string            `json:"userinfo_url"`
	RedirectURL            string            `json:"redirect_url"`
	ClientIDEnv            string            `json:"client_id_env"`
	ClientSecretEnv        string            `json:"client_secret_env"`
	TokenAuth              string            `json:"token_auth_method"`
	Scopes                 []string          `json:"scopes"`
	Claims                 Claims            `json:"claims"`
	RequireVerified        *bool             `json:"require_verified_email,omitempty"`
	AuthParams             map[string]string `json:"auth_params,omitempty"`
	AllowHTTP              bool              `json:"allow_http_loopback,omitempty"`
	clientID, clientSecret string
}

func Load(path string) ([]Provider, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var conf struct {
		Providers []Provider `json:"providers"`
	}
	if err = decoder.Decode(&conf); err != nil {
		return nil, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("expected one provider configuration")
	}
	seen := map[string]bool{}
	enabled := []Provider{}
	for _, provider := range conf.Providers {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`).MatchString(provider.ID) || seen[provider.ID] {
			return nil, errors.New("invalid or duplicate provider id")
		}
		seen[provider.ID] = true
		if !provider.Enabled {
			continue
		}
		if err = validate(&provider); err != nil {
			return nil, fmt.Errorf("provider %s: %w", provider.ID, err)
		}
		enabled = append(enabled, provider)
	}
	return enabled, nil
}

func validate(p *Provider) error {
	for _, raw := range []string{p.AuthorizeURL, p.TokenURL, p.UserinfoURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return errors.New("invalid provider endpoint")
		}
		loopback := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
		if u.Scheme != "https" && !(p.AllowHTTP && u.Scheme == "http" && loopback) {
			return errors.New("provider endpoints require HTTPS (HTTP loopback must be explicitly enabled)")
		}
	}
	redirect, err := url.Parse(p.RedirectURL)
	if err != nil || redirect.Host == "" || redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" || redirect.Path != "/qslate/v1/auth/oauth/"+p.ID+"/callback" {
		return errors.New("invalid provider redirect_url")
	}
	if redirect.Scheme != "https" && !(redirect.Scheme == "http" && (redirect.Hostname() == "localhost" || net.ParseIP(redirect.Hostname()).IsLoopback())) {
		return errors.New("redirect_url requires HTTPS outside localhost")
	}
	p.clientID = os.Getenv(p.ClientIDEnv)
	p.clientSecret = os.Getenv(p.ClientSecretEnv)
	if p.clientID == "" {
		return errors.New("client ID environment variable is empty")
	}
	if p.TokenAuth != "client_secret_basic" && p.TokenAuth != "client_secret_post" && p.TokenAuth != "none" {
		return errors.New("invalid token_auth_method")
	}
	if p.TokenAuth != "none" && p.clientSecret == "" {
		return errors.New("client secret environment variable is empty")
	}
	if p.Claims.Subject == "" || p.Claims.Email == "" || len(p.Scopes) == 0 {
		return errors.New("subject/email claim mapping and scopes required")
	}
	if (p.RequireVerified == nil || *p.RequireVerified) && p.Claims.Verified == "" {
		return errors.New("verified email claim mapping required")
	}
	return nil
}
