package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"qslate/services/auth/internal/domain"
	"qslate/services/auth/internal/platform/httpx"
	"qslate/services/auth/internal/platform/token"
	"qslate/services/auth/internal/service"
)

const RefreshCookie = "qslate_refresh"
const CookiePath = "/qslate/v1/auth"

type Config struct {
	SecureCookies  bool
	AllowedOrigins []string
}
type UseCases interface {
	Register(context.Context, string, string, string) (service.Session, error)
	Login(context.Context, string, string) (service.Session, error)
	Refresh(context.Context, string) (service.Session, error)
	Logout(context.Context, string) error
	ValidateSession(context.Context, int64, string) error
	Me(context.Context, int64) (domain.User, error)
	UpdateProfile(context.Context, int64, domain.ProfilePatch) (domain.User, error)
	Preferences(context.Context, int64) (domain.Preferences, error)
	UpdatePreferences(context.Context, int64, domain.PreferencesPatch) (domain.Preferences, error)
	RequestAction(context.Context, string, string) error
	VerifyEmail(context.Context, string) error
	ResetPassword(context.Context, string, string) error
	APIKeys(context.Context, int64) ([]domain.APIKey, error)
	CreateAPIKey(context.Context, int64, service.APIKeyInput) (service.NewAPIKey, error)
	RevokeAPIKey(context.Context, int64, string) error
}
type router struct {
	auth   UseCases
	cfg    Config
	logger *slog.Logger
}

func New(auth UseCases, verifier token.Verifier, ready func(context.Context) error, logger *slog.Logger, cfg Config, extra ...func(*http.ServeMux)) http.Handler {
	r := &router{auth: auth, cfg: cfg, logger: logger}
	mux := http.NewServeMux()
	httpx.Probes(mux, ready)
	mux.HandleFunc("POST /auth/register", r.register)
	mux.HandleFunc("POST /auth/login", r.login)
	mux.HandleFunc("POST /auth/refresh", r.refresh)
	mux.HandleFunc("POST /auth/logout", r.logout)
	mux.HandleFunc("POST /auth/forgot-password", r.forgot)
	mux.HandleFunc("POST /auth/reset-password", r.reset)
	mux.HandleFunc("POST /auth/verify-email", r.verify)
	private := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, token.Protect(verifier, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if err := auth.ValidateSession(req.Context(), token.Subject(req.Context()), token.SessionID(req.Context())); err != nil {
				r.fail(w, req, err)
				return
			}
			handler(w, req)
		})))
	}
	private("GET /users/me", r.me)
	private("PATCH /users/me", r.profile)
	private("GET /users/me/preferences", r.preferences)
	private("PATCH /users/me/preferences", r.patchPreferences)
	private("GET /users/me/api-keys", r.keys)
	private("POST /users/me/api-keys", r.createKey)
	private("DELETE /users/me/api-keys/{keyId}", r.revokeKey)
	for _, register := range extra {
		register(mux)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		origin := req.Header.Get("Origin")
		allowed := origin == ""
		for _, value := range cfg.AllowedOrigins {
			if origin == strings.TrimSpace(value) {
				allowed = true
			}
		}
		oauthCallback := req.Method == "GET" && strings.HasPrefix(req.URL.Path, "/auth/oauth/") && strings.HasSuffix(req.URL.Path, "/callback")
		if !allowed || (req.Header.Get("Sec-Fetch-Site") == "cross-site" && !oauthCallback) {
			httpx.Error(w, 403, "origin_forbidden", "origin is not allowed")
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, req.WithContext(ctx))
	})
}

func SetRefresh(w http.ResponseWriter, cfg Config, session service.Session) {
	maxAge := max(1, int(time.Until(session.RefreshExpiresAt).Seconds()))
	http.SetCookie(w, &http.Cookie{Name: RefreshCookie, Value: session.RefreshToken, Path: CookiePath, Expires: session.RefreshExpiresAt, MaxAge: maxAge, HttpOnly: true, Secure: cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
}
func (r *router) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: RefreshCookie, Path: CookiePath, MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: r.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
}
func (r *router) cookie(req *http.Request) string {
	cookie, err := req.Cookie(RefreshCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func (r *router) session(w http.ResponseWriter, req *http.Request, session service.Session, err error, status int) {
	if err != nil {
		r.fail(w, req, err)
		return
	}
	SetRefresh(w, r.cfg, session)
	httpx.JSON(w, status, session)
}

func (r *router) register(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	session, err := r.auth.Register(req.Context(), in.Email, in.Username, in.Password)
	r.session(w, req, session, err, 201)
}
func (r *router) login(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	session, err := r.auth.Login(req.Context(), in.Identifier, in.Password)
	r.session(w, req, session, err, 200)
}
func (r *router) cookieOrigin(w http.ResponseWriter, req *http.Request) bool {
	if req.Header.Get("Origin") == "" {
		httpx.Error(w, 403, "origin_required", "Origin header required for cookie-authenticated operations")
		return false
	}
	return true
}
func (r *router) refresh(w http.ResponseWriter, req *http.Request) {
	if !r.cookieOrigin(w, req) {
		return
	}
	session, err := r.auth.Refresh(req.Context(), r.cookie(req))
	if err != nil && errors.Is(err, domain.ErrCredentials) {
		r.clear(w)
	}
	r.session(w, req, session, err, 200)
}
func (r *router) logout(w http.ResponseWriter, req *http.Request) {
	if !r.cookieOrigin(w, req) {
		return
	}
	if err := r.auth.Logout(req.Context(), r.cookie(req)); err != nil {
		r.fail(w, req, err)
		return
	}
	r.clear(w)
	w.WriteHeader(204)
}
func (r *router) forgot(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	if err := r.auth.RequestAction(req.Context(), in.Email, "reset_password"); err != nil {
		r.fail(w, req, err)
		return
	}
	httpx.JSON(w, 202, map[string]string{"message": "If the account is eligible, an email will be sent."})
}
func (r *router) verify(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Token string `json:"token"`
		Email string `json:"email"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	if (in.Token == "") == (in.Email == "") {
		r.fail(w, req, domain.ErrInvalidInput)
		return
	}
	if in.Email != "" {
		if err := r.auth.RequestAction(req.Context(), in.Email, "verify_email"); err != nil {
			r.fail(w, req, err)
			return
		}
		httpx.JSON(w, 202, map[string]string{"message": "If the account is eligible, an email will be sent."})
		return
	}
	if err := r.auth.VerifyEmail(req.Context(), in.Token); err != nil {
		r.fail(w, req, err)
		return
	}
	w.WriteHeader(204)
}
func (r *router) reset(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	if err := r.auth.ResetPassword(req.Context(), in.Token, in.Password); err != nil {
		r.fail(w, req, err)
		return
	}
	r.clear(w)
	w.WriteHeader(204)
}
func (r *router) me(w http.ResponseWriter, req *http.Request) {
	user, err := r.auth.Me(req.Context(), token.Subject(req.Context()))
	if err != nil {
		r.fail(w, req, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"user": user})
}
func (r *router) profile(w http.ResponseWriter, req *http.Request) {
	var in domain.ProfilePatch
	if !httpx.Decode(w, req, &in) {
		return
	}
	user, err := r.auth.UpdateProfile(req.Context(), token.Subject(req.Context()), in)
	if err != nil {
		r.fail(w, req, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"user": user})
}
func (r *router) preferences(w http.ResponseWriter, req *http.Request) {
	prefs, err := r.auth.Preferences(req.Context(), token.Subject(req.Context()))
	if err != nil {
		r.fail(w, req, err)
		return
	}
	httpx.JSON(w, 200, prefs)
}
func (r *router) patchPreferences(w http.ResponseWriter, req *http.Request) {
	var in domain.PreferencesPatch
	if !httpx.Decode(w, req, &in) {
		return
	}
	prefs, err := r.auth.UpdatePreferences(req.Context(), token.Subject(req.Context()), in)
	if err != nil {
		r.fail(w, req, err)
		return
	}
	httpx.JSON(w, 200, prefs)
}
func (r *router) keys(w http.ResponseWriter, req *http.Request) {
	keys, err := r.auth.APIKeys(req.Context(), token.Subject(req.Context()))
	if err != nil {
		r.fail(w, req, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": keys})
}
func (r *router) createKey(w http.ResponseWriter, req *http.Request) {
	var in service.APIKeyInput
	if !httpx.Decode(w, req, &in) {
		return
	}
	key, err := r.auth.CreateAPIKey(req.Context(), token.Subject(req.Context()), in)
	if err != nil {
		r.fail(w, req, err)
		return
	}
	httpx.JSON(w, 201, key)
}
func (r *router) revokeKey(w http.ResponseWriter, req *http.Request) {
	if err := r.auth.RevokeAPIKey(req.Context(), token.Subject(req.Context()), req.PathValue("keyId")); err != nil {
		r.fail(w, req, err)
		return
	}
	w.WriteHeader(204)
}

func (r *router) fail(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.Error(w, 400, "invalid_input", err.Error())
	case errors.Is(err, domain.ErrActionToken):
		httpx.Error(w, 400, "invalid_token", domain.ErrActionToken.Error())
	case errors.Is(err, domain.ErrConflict):
		httpx.Error(w, 409, "account_exists", domain.ErrConflict.Error())
	case errors.Is(err, domain.ErrCredentials):
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpx.Error(w, 401, "invalid_credentials", "invalid credentials or revoked session")
	case errors.Is(err, domain.ErrNotFound):
		httpx.Error(w, 404, "not_found", "resource not found")
	default:
		r.logger.Error("auth operation failed", "error", err, "request_id", httpx.RequestID(req.Context()))
		httpx.Error(w, 500, "internal_error", "internal server error")
	}
}
