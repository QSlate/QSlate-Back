package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"qslate/services/workspace/internal/domain"
	"qslate/services/workspace/internal/platform/httpx"
	"qslate/services/workspace/internal/platform/session"
	"qslate/services/workspace/internal/platform/token"
	"qslate/services/workspace/internal/service"
)

type Sessions interface {
	Validate(context.Context, string, int64) error
}
type router struct {
	service *service.Dashboards
	logger  *slog.Logger
}

func New(svc *service.Dashboards, verifier token.Verifier, sessions Sessions, ready func(context.Context) error, logger *slog.Logger) http.Handler {
	r := &router{service: svc, logger: logger}
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
	private("GET /dashboards", r.list)
	private("POST /dashboards", r.create)
	private("GET /dashboards/{dashboardId}", r.get)
	private("PUT /dashboards/{dashboardId}", r.replace)
	private("DELETE /dashboards/{dashboardId}", r.delete)
	private("POST /dashboards/{dashboardId}/duplicate", r.duplicate)
	private("PUT /dashboards/{dashboardId}/layout", r.layout)
	private("POST /dashboards/{dashboardId}/widgets", r.addWidget)
	private("PATCH /dashboards/{dashboardId}/widgets/{instanceId}", r.patchWidget)
	private("DELETE /dashboards/{dashboardId}/widgets/{instanceId}", r.deleteWidget)
	private("POST /dashboards/{dashboardId}/layout/reset", r.reset)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, req.WithContext(ctx))
	})
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
func (r *router) fail(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.Error(w, 400, "invalid_input", err.Error())
	case errors.Is(err, domain.ErrNotFound):
		httpx.Error(w, 404, "not_found", "dashboard or widget not found")
	case errors.Is(err, domain.ErrUnauthorized), errors.Is(err, session.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpx.Error(w, 401, "unauthorized", "active session required")
	case errors.Is(err, domain.ErrConflict):
		httpx.Error(w, 409, "revision_conflict", "dashboard changed; reload it before saving the layout")
	case errors.Is(err, session.ErrUnavailable):
		httpx.Error(w, 503, "auth_unavailable", "session authority unavailable")
	case errors.Is(err, domain.ErrCatalogueUnavailable):
		httpx.Error(w, 503, "catalogue_unavailable", "widget catalogue unavailable")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		httpx.Error(w, 503, "unavailable", "request could not complete")
	default:
		r.logger.Error("workspace request failed", "request_id", httpx.RequestID(req.Context()), "error", err)
		httpx.Error(w, 500, "internal_error", "internal server error")
	}
}

func owner(req *http.Request) int64        { return token.Subject(req.Context()) }
func dashboardID(req *http.Request) string { return req.PathValue("dashboardId") }

// Raw optional values distinguish omitted properties from explicit JSON null.
func decodeMetadata(w http.ResponseWriter, req *http.Request) (domain.Metadata, bool) {
	var raw struct {
		Title       string          `json:"title"`
		Description json.RawMessage `json:"description"`
		Tags        json.RawMessage `json:"tags"`
	}
	var input domain.Metadata
	if !httpx.Decode(w, req, &raw) {
		return input, false
	}
	input.Title = raw.Title
	for _, field := range []struct {
		raw    json.RawMessage
		target any
	}{{raw.Description, &input.Description}, {raw.Tags, &input.Tags}} {
		if len(field.raw) == 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(field.raw), []byte("null")) || json.Unmarshal(field.raw, field.target) != nil {
			httpx.Error(w, 400, "invalid_input", "description must be a string and tags an array of strings; null is not allowed")
			return input, false
		}
	}
	return input, true
}

func (r *router) list(w http.ResponseWriter, req *http.Request) {
	query, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		httpx.Error(w, 400, "invalid_input", "malformed query string")
		return
	}
	limit := 0
	for key, values := range query {
		if (key != "limit" && key != "cursor") || len(values) != 1 {
			httpx.Error(w, 400, "invalid_input", "unsupported or repeated query parameter")
			return
		}
	}
	if raw, exists := query["limit"]; exists {
		limit, err = strconv.Atoi(raw[0])
		if err != nil || limit < 1 {
			httpx.Error(w, 400, "invalid_input", "limit must be an integer from 1 to 1000")
			return
		}
	}
	result, err := r.service.List(req.Context(), owner(req), query.Get("cursor"), limit)
	r.respond(w, req, 200, result, err)
}
func (r *router) create(w http.ResponseWriter, req *http.Request) {
	input, ok := decodeMetadata(w, req)
	if !ok {
		return
	}
	result, err := r.service.Create(req.Context(), owner(req), input)
	r.respond(w, req, 201, result, err)
}
func (r *router) get(w http.ResponseWriter, req *http.Request) {
	result, err := r.service.Get(req.Context(), owner(req), dashboardID(req))
	r.respond(w, req, 200, result, err)
}
func (r *router) replace(w http.ResponseWriter, req *http.Request) {
	input, ok := decodeMetadata(w, req)
	if !ok {
		return
	}
	result, err := r.service.ReplaceMetadata(req.Context(), owner(req), dashboardID(req), input)
	r.respond(w, req, 200, result, err)
}
func (r *router) delete(w http.ResponseWriter, req *http.Request) {
	r.respond(w, req, 204, nil, r.service.Delete(req.Context(), owner(req), dashboardID(req)))
}
func (r *router) duplicate(w http.ResponseWriter, req *http.Request) {
	result, err := r.service.Duplicate(req.Context(), owner(req), dashboardID(req))
	r.respond(w, req, 201, result, err)
}
func (r *router) layout(w http.ResponseWriter, req *http.Request) {
	var input struct {
		Revision int64 `json:"revision"`
		Items    []struct {
			InstanceID string `json:"instance_id"`
			X          *int   `json:"x"`
			Y          *int   `json:"y"`
			Width      *int   `json:"width"`
			Height     *int   `json:"height"`
		} `json:"items"`
	}
	if !httpx.Decode(w, req, &input) {
		return
	}
	if input.Items == nil {
		httpx.Error(w, 400, "invalid_input", "items array is required")
		return
	}
	items := make([]domain.LayoutItem, 0, len(input.Items))
	for _, item := range input.Items {
		if item.X == nil || item.Y == nil || item.Width == nil || item.Height == nil {
			httpx.Error(w, 400, "invalid_input", "all layout coordinates and dimensions are required")
			return
		}
		items = append(items, domain.LayoutItem{InstanceID: item.InstanceID, X: *item.X, Y: *item.Y, Width: *item.Width, Height: *item.Height})
	}
	result, err := r.service.SaveLayout(req.Context(), owner(req), dashboardID(req), input.Revision, items)
	r.respond(w, req, 200, result, err)
}
func (r *router) addWidget(w http.ResponseWriter, req *http.Request) {
	var input struct {
		WidgetID      string          `json:"widget_id"`
		VersionID     string          `json:"widget_version_id"`
		Configuration json.RawMessage `json:"configuration"`
	}
	if !httpx.Decode(w, req, &input) {
		return
	}
	result, err := r.service.AddWidget(req.Context(), owner(req), dashboardID(req), input.WidgetID, input.VersionID, input.Configuration)
	r.respond(w, req, 201, result, err)
}
func (r *router) patchWidget(w http.ResponseWriter, req *http.Request) {
	var input struct {
		Configuration json.RawMessage `json:"configuration"`
	}
	if !httpx.Decode(w, req, &input) {
		return
	}
	result, err := r.service.PatchWidget(req.Context(), owner(req), dashboardID(req), req.PathValue("instanceId"), input.Configuration)
	r.respond(w, req, 200, result, err)
}
func (r *router) deleteWidget(w http.ResponseWriter, req *http.Request) {
	r.respond(w, req, 204, nil, r.service.DeleteWidget(req.Context(), owner(req), dashboardID(req), req.PathValue("instanceId")))
}
func (r *router) reset(w http.ResponseWriter, req *http.Request) {
	result, err := r.service.ResetLayout(req.Context(), owner(req), dashboardID(req))
	r.respond(w, req, 200, result, err)
}
