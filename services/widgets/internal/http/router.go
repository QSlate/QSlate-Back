package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"qslate/services/widgets/internal/domain"
	"qslate/services/widgets/internal/marketplace"
	"qslate/services/widgets/internal/platform/httpx"
	"qslate/services/widgets/internal/platform/session"
	"qslate/services/widgets/internal/platform/token"
	"qslate/services/widgets/internal/service"
	"sort"
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
	market *marketplace.Client
}

func New(svc *service.Widgets, cfg service.Config, verifier token.Verifier, sessions Sessions, ready func(context.Context) error, logger *slog.Logger, markets ...*marketplace.Client) http.Handler {
	r := &router{svc: svc, cfg: cfg, logger: logger}
	if len(markets) > 0 {
		r.market = markets[0]
	}
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
	private("POST /catalogue/snapshot", r.snapshot)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, req.WithContext(ctx))
	})
}

func (r *router) snapshot(w http.ResponseWriter, req *http.Request) {
	var in struct {
		ResourceID string `json:"resource_id"`
		VersionID  string `json:"version_id"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	v, err := r.svc.Snapshot(req.Context(), uid(req), in.ResourceID, in.VersionID)
	r.respond(w, req, 200, v, err)
}
func (r *router) fail(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, marketplace.ErrUnavailable):
		httpx.Error(w, 503, "marketplace_unavailable", "installed catalogue unavailable")
	case errors.Is(err, marketplace.ErrNotFound):
		httpx.Error(w, 404, "not_found", "installation not found")
	case errors.Is(err, marketplace.ErrUnauthorized):
		httpx.Error(w, 401, "unauthorized", "active session required")
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
	if err == nil && installed && r.market != nil {
		if limit == 0 {
			limit = 100
		}
		remote, e := r.market.List(req.Context(), "widgets", q.Get("cursor"), limit)
		if e != nil {
			r.fail(w, req, e)
			return
		}
		more := page.NextCursor != "" || remote.NextCursor != ""
		for _, v := range remote.Items {
			page.Items = append(page.Items, domain.Summary{ID: v.CatalogID, VersionID: v.VersionID, Kind: "marketplace", Name: v.Name, Language: v.Language, CreatedAt: v.InstalledAt, UpdatedAt: v.InstalledAt})
		}
		sort.Slice(page.Items, func(i, j int) bool { return page.Items[i].ID > page.Items[j].ID })
		if len(page.Items) > limit {
			page.Items = page.Items[:limit]
			more = true
		}
		page.NextCursor = ""
		if more && len(page.Items) > 0 {
			page.NextCursor = page.Items[len(page.Items)-1].ID
		}
	}
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
	if marketplace.IsReference(in.WidgetID) {
		v, e := r.market.Resolve(req.Context(), in.WidgetID, in.VersionID, in.Existing)
		if e == nil && (v.Kind != "widget" && v.Kind != "indicator") {
			e = marketplace.ErrNotFound
		}
		r.respond(w, req, 200, domain.Reference{WidgetID: in.WidgetID, VersionID: in.VersionID}, e)
		return
	}
	result, err := r.svc.Resolve(req.Context(), uid(req), in.WidgetID, in.VersionID, in.Existing)
	r.respond(w, req, 200, result, err)
}
