package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"qslate/internal/platform/httpx"
	"qslate/services/auth/internal/domain"
	authhttp "qslate/services/auth/internal/http"
	"qslate/services/auth/internal/service"
)

type Store interface {
	SaveOAuthState(context.Context, string, string, []byte, time.Time) error
	ConsumeOAuthState(context.Context, string, string) ([]byte, error)
}
type Cipher interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}
type Login interface {
	ExternalLogin(context.Context, domain.ExternalIdentity) (service.Session, error)
}
type Handler struct {
	providers map[string]Provider
	store     Store
	cipher    Cipher
	login     Login
	cfg       authhttp.Config
	appURL    string
	client    *http.Client
}

func New(providers []Provider, store Store, cipher Cipher, login Login, cfg authhttp.Config, appURL string) *Handler {
	h := &Handler{providers: map[string]Provider{}, store: store, cipher: cipher, login: login, cfg: cfg, appURL: strings.TrimRight(appURL, "/"), client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for _, p := range providers {
		h.providers[p.ID] = p
	}
	return h
}
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/providers", h.list)
	mux.HandleFunc("GET /auth/oauth/{provider}/authorize", h.authorize)
	mux.HandleFunc("GET /auth/oauth/{provider}/callback", h.callback)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	items := []map[string]string{}
	for _, p := range h.providers {
		items = append(items, map[string]string{"id": p.ID, "display_name": p.DisplayName, "authorize_path": "/qslate/v1/auth/oauth/" + p.ID + "/authorize"})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["id"] < items[j]["id"] })
	httpx.JSON(w, 200, map[string]any{"items": items})
}

const cookieName = "qslate_oauth_state"
const cookiePath = "/qslate/v1/auth/oauth"

func random() string {
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	p, ok := h.providers[r.PathValue("provider")]
	if !ok {
		httpx.Error(w, 404, "unknown_provider", "provider not enabled")
		return
	}
	state, verifier := random(), random()
	sealed, err := h.cipher.Encrypt([]byte(verifier))
	if err != nil {
		httpx.Error(w, 500, "oauth_unavailable", "authentication unavailable")
		return
	}
	if err = h.store.SaveOAuthState(r.Context(), hash(state), p.ID, sealed, time.Now().Add(10*time.Minute)); err != nil {
		httpx.Error(w, 503, "oauth_unavailable", "authentication unavailable")
		return
	}
	challenge := sha256.Sum256([]byte(verifier))
	target, _ := url.Parse(p.AuthorizeURL)
	query := target.Query()
	for key, value := range p.AuthParams {
		query.Set(key, value)
	}
	query.Set("response_type", "code")
	query.Set("client_id", p.clientID)
	query.Set("redirect_uri", p.RedirectURL)
	query.Set("scope", strings.Join(p.Scopes, " "))
	query.Set("state", state)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	target.RawQuery = query.Encode()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: state, Path: cookiePath, MaxAge: 600, HttpOnly: true, Secure: h.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, target.String(), http.StatusFound)
}
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	p, ok := h.providers[r.PathValue("provider")]
	if !ok {
		httpx.Error(w, 404, "unknown_provider", "provider not enabled")
		return
	}
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(cookieName)
	if err != nil || len(state) != 43 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
		httpx.Error(w, 400, "invalid_oauth_state", "invalid or expired authorization state")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: cookiePath, MaxAge: -1, HttpOnly: true, Secure: h.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	sealed, err := h.store.ConsumeOAuthState(r.Context(), hash(state), p.ID)
	if err != nil {
		httpx.Error(w, 400, "invalid_oauth_state", "invalid or expired authorization state")
		return
	}
	verifier, err := h.cipher.Decrypt(sealed)
	if err != nil {
		httpx.Error(w, 500, "oauth_unavailable", "authentication unavailable")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" || len(code) > 4096 {
		httpx.Error(w, 400, "oauth_denied", "authorization was not completed")
		return
	}
	identity, err := h.exchange(r.Context(), p, code, string(verifier))
	if err != nil {
		httpx.Error(w, 401, "oauth_failed", "provider identity could not be verified")
		return
	}
	session, err := h.login.ExternalLogin(r.Context(), identity)
	if errors.Is(err, domain.ErrConflict) {
		httpx.Error(w, 409, "account_link_required", "an existing account uses this email; automatic linking is disabled")
		return
	}
	if err != nil {
		httpx.Error(w, 503, "oauth_unavailable", "authentication unavailable")
		return
	}
	authhttp.SetRefresh(w, h.cfg, session)
	// Never put access/refresh tokens in a URL. The frontend obtains access via POST /auth/refresh.
	http.Redirect(w, r, h.appURL+"/auth/callback?status=success", http.StatusSeeOther)
}

func (h *Handler) exchange(ctx context.Context, p Provider, code, verifier string) (domain.ExternalIdentity, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {p.RedirectURL}, "client_id": {p.clientID}, "code_verifier": {verifier}}
	if p.TokenAuth == "client_secret_post" {
		form.Set("client_secret", p.clientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, "POST", p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return domain.ExternalIdentity{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if p.TokenAuth == "client_secret_basic" {
		request.SetBasicAuth(url.QueryEscape(p.clientID), url.QueryEscape(p.clientSecret))
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err = h.fetch(request, &tokens); err != nil {
		return domain.ExternalIdentity{}, err
	}
	if tokens.AccessToken == "" || !strings.EqualFold(tokens.TokenType, "Bearer") {
		return domain.ExternalIdentity{}, errors.New("missing bearer access token")
	}
	request, err = http.NewRequestWithContext(ctx, "GET", p.UserinfoURL, nil)
	if err != nil {
		return domain.ExternalIdentity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	request.Header.Set("Accept", "application/json")
	var info map[string]any
	if err = h.fetch(request, &info); err != nil {
		return domain.ExternalIdentity{}, err
	}
	verified, _ := claim(info, p.Claims.Verified).(bool)
	if (p.RequireVerified == nil || *p.RequireVerified) && !verified {
		return domain.ExternalIdentity{}, errors.New("verified email required")
	}
	subject, _ := claim(info, p.Claims.Subject).(string)
	if number, ok := claim(info, p.Claims.Subject).(json.Number); ok {
		subject = number.String()
	}
	email, _ := claim(info, p.Claims.Email).(string)
	name, _ := claim(info, p.Claims.Name).(string)
	if subject == "" || email == "" {
		return domain.ExternalIdentity{}, errors.New("identity claims missing")
	}
	return domain.ExternalIdentity{Provider: p.ID, Subject: subject, Email: email, DisplayName: name, EmailVerified: verified}, nil
}
func (h *Handler) fetch(request *http.Request, out any) error {
	response, err := h.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("provider request rejected")
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.UseNumber()
	return decoder.Decode(out)
}
func claim(info map[string]any, path string) any {
	var value any = info
	for _, key := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}
