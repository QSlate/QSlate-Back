package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"qslate/internal/platform/httpx"
	"qslate/internal/platform/session"
	"qslate/internal/platform/token"
	"qslate/services/widgets/internal/domain"
	"qslate/services/widgets/internal/service"
	"strconv"
	"time"
)

type Sessions interface {
	Validate(context.Context, string, int64) error
}
type router struct {
	svc    *service.Widgets
	cfg    service.Config
	logger *slog.Logger
}

func New(svc *service.Widgets, cfg service.Config, verifier token.Verifier, sessions Sessions, ready func(context.Context) error, logger *slog.Logger) http.Handler {
	r := &router{svc, cfg, logger}
	mux := http.NewServeMux()
	httpx.Probes(mux, ready)
	private := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, token.Protect(verifier, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if err := sessions.Validate(req.Context(), req.Header.Get("Authorization"), token.Subject(req.Context())); err != nil {
				r.fail(w, req, err)
				return
			}
			handler(w, req)
		})))
	}
	private("GET /widgets/installed", func(w http.ResponseWriter, req *http.Request) { r.list(w, req, true) })
	private("GET /widgets/custom", func(w http.ResponseWriter, req *http.Request) { r.list(w, req, false) })
	private("POST /widgets/custom", r.create)
	private("GET /widgets/custom/{widgetId}", r.get)
	private("PUT /widgets/custom/{widgetId}", r.replace)
	private("DELETE /widgets/custom/{widgetId}", r.delete)
	private("POST /widgets/custom/{widgetId}/preview", r.preview)
	// Private catalogue contract for Workspace; deliberately absent from the gateway.
	private("POST /catalogue/resolve", r.resolve)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, req.WithContext(ctx))
	})
}
func (r *router) fail(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		httpx.Error(w, 400, "invalid_input", err.Error())
	case errors.Is(err, domain.ErrNotFound):
		httpx.Error(w, 404, "not_found", "widget or version not found")
	case errors.Is(err, domain.ErrUnauthorized), errors.Is(err, session.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpx.Error(w, 401, "unauthorized", "active session required")
	case errors.Is(err, domain.ErrUnavailable):
		httpx.Error(w, 503, "preview_unavailable", "sandbox preview controller unavailable")
	case errors.Is(err, session.ErrUnavailable):
		httpx.Error(w, 503, "auth_unavailable", "session authority unavailable")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		httpx.Error(w, 503, "unavailable", "request could not complete")
	default:
		r.logger.Error("Widgets request failed", "request_id", httpx.RequestID(req.Context()), "error", err)
		httpx.Error(w, 500, "internal_error", "internal server error")
	}
}
func (r *router) respond(w http.ResponseWriter, req *http.Request, status int, result any, err error) {
	if err != nil {
		r.fail(w, req, err)
		return
	}
	if status == 204 {
		w.WriteHeader(status)
		return
	}
	httpx.JSON(w, status, result)
}
func uid(req *http.Request) int64 { return token.Subject(req.Context()) }
func (r *router) list(w http.ResponseWriter, req *http.Request, installed bool) {
	q, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		r.fail(w, req, domain.ErrInvalid)
		return
	}
	limit := 0
	for key, values := range q {
		if len(values) != 1 || (key != "limit" && key != "cursor") {
			r.fail(w, req, domain.ErrInvalid)
			return
		}
	}
	if raw, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(raw[0])
		if err != nil || limit < 1 {
			r.fail(w, req, domain.ErrInvalid)
			return
		}
	}
	page, err := r.svc.List(req.Context(), uid(req), installed, q.Get("cursor"), limit)
	r.respond(w, req, 200, page, err)
}
func (r *router) create(w http.ResponseWriter, req *http.Request) {
	var in domain.Input
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	result, err := r.svc.Create(req.Context(), uid(req), in)
	r.respond(w, req, 201, result, err)
}
func (r *router) get(w http.ResponseWriter, req *http.Request) {
	result, err := r.svc.Get(req.Context(), uid(req), req.PathValue("widgetId"))
	r.respond(w, req, 200, result, err)
}
func (r *router) replace(w http.ResponseWriter, req *http.Request) {
	var in domain.Input
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	result, err := r.svc.Replace(req.Context(), uid(req), req.PathValue("widgetId"), in)
	r.respond(w, req, 200, result, err)
}
func (r *router) delete(w http.ResponseWriter, req *http.Request) {
	r.respond(w, req, 204, nil, r.svc.Delete(req.Context(), uid(req), req.PathValue("widgetId")))
}
func (r *router) preview(w http.ResponseWriter, req *http.Request) {
	var in domain.PreviewInput
	if !httpx.Decode(w, req, &in) {
		return
	}
	result, err := r.svc.Preview(req.Context(), uid(req), req.PathValue("widgetId"), req.Header.Get("Idempotency-Key"), in)
	r.respond(w, req, 202, result, err)
}
func (r *router) resolve(w http.ResponseWriter, req *http.Request) {
	var in struct {
		WidgetID  string `json:"widget_id"`
		VersionID string `json:"widget_version_id"`
		Existing  bool   `json:"existing"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	result, err := r.svc.Resolve(req.Context(), uid(req), in.WidgetID, in.VersionID, in.Existing)
	r.respond(w, req, 200, result, err)
}
