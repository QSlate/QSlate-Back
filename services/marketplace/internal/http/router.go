package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"qslate/services/marketplace/internal/domain"
	"qslate/services/marketplace/internal/platform/httpx"
	"qslate/services/marketplace/internal/platform/session"
	"qslate/services/marketplace/internal/platform/token"
	"qslate/services/marketplace/internal/service"
	"sort"
	"strconv"
	"time"
)

type Sessions interface {
	Validate(context.Context, string, int64) error
}
type router struct {
	svc    *service.Marketplace
	cfg    service.Config
	logger *slog.Logger
}

func New(svc *service.Marketplace, cfg service.Config, verifier token.Verifier, sessions Sessions, ready func(context.Context) error, logger *slog.Logger) http.Handler {
	r := &router{svc, cfg, logger}
	mux := http.NewServeMux()
	httpx.Probes(mux, ready)
	private := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, token.Protect(verifier, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if e := sessions.Validate(req.Context(), token.Authorization(req.Context()), token.Subject(req.Context())); e != nil {
				r.fail(w, req, e)
				return
			}
			h(w, req)
		})))
	}
	mux.HandleFunc("GET /marketplace/items", r.list)
	mux.HandleFunc("GET /marketplace/featured", r.list)
	mux.HandleFunc("GET /marketplace/categories", r.categories)
	mux.HandleFunc("GET /marketplace/items/{itemId}", r.get)
	mux.HandleFunc("GET /marketplace/items/{itemId}/reviews", r.reviews)
	private("POST /marketplace/items", r.publish)
	private("PATCH /marketplace/items/{itemId}", r.patch)
	private("DELETE /marketplace/items/{itemId}", r.delete)
	private("POST /marketplace/items/{itemId}/install", r.install)
	private("DELETE /marketplace/items/{itemId}/installation", r.uninstall)
	private("POST /marketplace/items/{itemId}/reviews", r.review)
	// Internal contracts, deliberately absent from gateway routes.
	private("GET /catalogue/installations", r.installations)
	private("POST /catalogue/resolve", r.resolve)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, req.WithContext(ctx))
	})
}
func (r *router) fail(w http.ResponseWriter, req *http.Request, e error) {
	switch {
	case errors.Is(e, domain.ErrInvalid):
		httpx.Error(w, 400, "invalid_input", e.Error())
	case errors.Is(e, domain.ErrNotFound):
		httpx.Error(w, 404, "not_found", "item or version not found")
	case errors.Is(e, domain.ErrConflict):
		httpx.Error(w, 409, "conflict", "listing changed, publication or review already exists")
	case errors.Is(e, domain.ErrForbidden):
		httpx.Error(w, 403, "forbidden", "an installed item from another author is required")
	case errors.Is(e, domain.ErrUnauthorized), errors.Is(e, session.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpx.Error(w, 401, "unauthorized", "active session required")
	case errors.Is(e, domain.ErrUnavailable), errors.Is(e, session.ErrUnavailable), errors.Is(e, context.DeadlineExceeded), errors.Is(e, context.Canceled):
		httpx.Error(w, 503, "dependency_unavailable", "required service unavailable")
	default:
		r.logger.Error("Marketplace request failed", "request_id", httpx.RequestID(req.Context()), "error", e)
		httpx.Error(w, 500, "internal_error", "internal server error")
	}
}
func (r *router) respond(w http.ResponseWriter, req *http.Request, status int, value any, e error) {
	if e != nil {
		r.fail(w, req, e)
		return
	}
	if status == 204 {
		w.WriteHeader(status)
		return
	}
	httpx.JSON(w, status, value)
}
func query(req *http.Request, allowed ...string) (url.Values, int, error) {
	q, e := url.ParseQuery(req.URL.RawQuery)
	if e != nil {
		return nil, 0, domain.ErrInvalid
	}
	keys := map[string]bool{"limit": true, "cursor": true}
	for _, k := range allowed {
		keys[k] = true
	}
	for k, v := range q {
		if !keys[k] || len(v) != 1 {
			return nil, 0, domain.ErrInvalid
		}
	}
	n := 0
	if v, ok := q["limit"]; ok {
		n, e = strconv.Atoi(v[0])
		if e != nil || n < 1 {
			return nil, 0, domain.ErrInvalid
		}
	}
	return q, n, nil
}
func (r *router) list(w http.ResponseWriter, req *http.Request) {
	q, n, e := query(req, "q", "kind", "category")
	if e != nil {
		r.fail(w, req, e)
		return
	}
	v, e := r.svc.List(req.Context(), domain.Filter{Query: q.Get("q"), Kind: q.Get("kind"), Category: q.Get("category"), Cursor: q.Get("cursor"), Limit: n, Featured: req.URL.Path == "/marketplace/featured"})
	r.respond(w, req, 200, v, e)
}
func (r *router) categories(w http.ResponseWriter, req *http.Request) {
	q, n, e := query(req)
	if e != nil {
		r.fail(w, req, e)
		return
	}
	if n == 0 {
		n = r.cfg.DefaultPageSize
	}
	if n > r.cfg.MaxPageSize {
		r.fail(w, req, domain.ErrInvalid)
		return
	}
	items := append([]service.Category(nil), r.cfg.Categories...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	cursor := q.Get("cursor")
	if cursor != "" {
		found := false
		for _, c := range items {
			found = found || c.ID == cursor
		}
		if !found {
			r.fail(w, req, domain.ErrInvalid)
			return
		}
	}
	out := []service.Category{}
	for _, c := range items {
		if cursor == "" || c.ID < cursor {
			out = append(out, c)
		}
	}
	next := ""
	if len(out) > n {
		out = out[:n]
		next = out[n-1].ID
	}
	httpx.JSON(w, 200, struct {
		Items []service.Category `json:"items"`
		Next  string             `json:"next_cursor,omitempty"`
	}{out, next})
}
func (r *router) get(w http.ResponseWriter, req *http.Request) {
	v, e := r.svc.Get(req.Context(), req.PathValue("itemId"))
	r.respond(w, req, 200, v, e)
}
func (r *router) publish(w http.ResponseWriter, req *http.Request) {
	var in domain.PublishInput
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	v, e := r.svc.Publish(req.Context(), token.Subject(req.Context()), in)
	r.respond(w, req, 201, v, e)
}
func (r *router) patch(w http.ResponseWriter, req *http.Request) {
	var in domain.Patch
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	v, e := r.svc.Patch(req.Context(), token.Subject(req.Context()), req.PathValue("itemId"), in)
	r.respond(w, req, 200, v, e)
}
func (r *router) delete(w http.ResponseWriter, req *http.Request) {
	r.respond(w, req, 204, nil, r.svc.Delete(req.Context(), token.Subject(req.Context()), req.PathValue("itemId")))
}
func (r *router) install(w http.ResponseWriter, req *http.Request) {
	var in struct {
		VersionID string `json:"version_id"`
	}
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	v, e := r.svc.Install(req.Context(), token.Subject(req.Context()), req.PathValue("itemId"), in.VersionID)
	r.respond(w, req, 201, v, e)
}
func (r *router) uninstall(w http.ResponseWriter, req *http.Request) {
	r.respond(w, req, 204, nil, r.svc.Uninstall(req.Context(), token.Subject(req.Context()), req.PathValue("itemId")))
}
func (r *router) reviews(w http.ResponseWriter, req *http.Request) {
	q, n, e := query(req)
	if e != nil {
		r.fail(w, req, e)
		return
	}
	v, e := r.svc.Reviews(req.Context(), req.PathValue("itemId"), q.Get("cursor"), n)
	r.respond(w, req, 200, v, e)
}
func (r *router) review(w http.ResponseWriter, req *http.Request) {
	var in domain.ReviewInput
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	v, e := r.svc.Review(req.Context(), token.Subject(req.Context()), req.PathValue("itemId"), in)
	r.respond(w, req, 201, v, e)
}
func (r *router) installations(w http.ResponseWriter, req *http.Request) {
	q, n, e := query(req, "kind")
	if e != nil {
		r.fail(w, req, e)
		return
	}
	v, e := r.svc.Installations(req.Context(), token.Subject(req.Context()), q.Get("kind"), q.Get("cursor"), n)
	if e != nil {
		r.fail(w, req, e)
		return
	}
	// Summaries stay bounded; source is only returned by the exact private resolver.
	type summary struct {
		ItemID      string    `json:"item_id"`
		VersionID   string    `json:"version_id"`
		Kind        string    `json:"kind"`
		CatalogID   string    `json:"catalog_id"`
		Name        string    `json:"name"`
		Language    string    `json:"language"`
		InstalledAt time.Time `json:"installed_at"`
	}
	out := []summary{}
	for _, i := range v.Items {
		out = append(out, summary{i.ItemID, i.VersionID, i.Kind, i.CatalogID, i.Name, i.Language, i.InstalledAt})
	}
	httpx.JSON(w, 200, struct {
		Items []summary `json:"items"`
		Next  string    `json:"next_cursor,omitempty"`
	}{out, v.NextCursor})
}
func (r *router) resolve(w http.ResponseWriter, req *http.Request) {
	var in struct {
		CatalogID string `json:"catalog_id"`
		VersionID string `json:"version_id"`
		Existing  bool   `json:"existing"`
	}
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	v, e := r.svc.Installed(req.Context(), token.Subject(req.Context()), in.CatalogID, in.VersionID, in.Existing)
	r.respond(w, req, 200, v, e)
}
