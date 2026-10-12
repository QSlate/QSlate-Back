package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"qslate/internal/platform/httpx"
	"qslate/internal/platform/token"
	"qslate/services/auth/internal/domain"
	"qslate/services/auth/internal/service"
)

type UseCases interface {
	Register(context.Context, string, string, string) (service.Session, error)
	Login(context.Context, string, string) (service.Session, error)
	Me(context.Context, int64) (domain.User, error)
}

func New(useCases UseCases, verifier token.Verifier, ready func(context.Context) error, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	httpx.Probes(mux, ready)
	mux.HandleFunc("POST /auth/register", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Email    string `json:"email"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !httpx.Decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		session, err := useCases.Register(ctx, input.Email, input.Username, input.Password)
		if err != nil {
			writeError(w, r, err, logger)
			return
		}
		httpx.JSON(w, 201, session)
	})
	mux.HandleFunc("POST /auth/login", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Identifier string `json:"identifier"`
			Password   string `json:"password"`
		}
		if !httpx.Decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		session, err := useCases.Login(ctx, input.Identifier, input.Password)
		if err != nil {
			writeError(w, r, err, logger)
			return
		}
		httpx.JSON(w, 200, session)
	})
	mux.Handle("GET /auth/me", token.Protect(verifier, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		user, err := useCases.Me(ctx, token.Subject(r.Context()))
		if err != nil {
			writeError(w, r, err, logger)
			return
		}
		httpx.JSON(w, 200, map[string]any{"user": user})
	})))
	return mux
}

func writeError(w http.ResponseWriter, r *http.Request, err error, logger *slog.Logger) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.Error(w, 400, "invalid_input", err.Error())
	case errors.Is(err, domain.ErrConflict):
		httpx.Error(w, 409, "account_exists", domain.ErrConflict.Error())
	case errors.Is(err, domain.ErrCredentials):
		httpx.Error(w, 401, "invalid_credentials", domain.ErrCredentials.Error())
	case errors.Is(err, domain.ErrNotFound):
		httpx.Error(w, 404, "user_not_found", "user not found")
	default:
		logger.Error("auth operation failed", "error", err, "request_id", httpx.RequestID(r.Context()))
		httpx.Error(w, 500, "internal_error", "internal server error")
	}
}
