package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"qslate/services/strategy/internal/domain"
	"qslate/services/strategy/internal/platform/httpx"
	"qslate/services/strategy/internal/platform/session"
	"qslate/services/strategy/internal/platform/token"
	"qslate/services/strategy/internal/service"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Sessions interface {
	Validate(context.Context, string, int64) error
}
type router struct {
	svc    *service.Strategies
	cfg    service.Config
	logger *slog.Logger
}

func New(svc *service.Strategies, cfg service.Config, verifier token.Verifier, sessions Sessions, ready func(context.Context) error, logger *slog.Logger) http.Handler {
	r := &router{svc, cfg, logger}
	mux := http.NewServeMux()
	httpx.Probes(mux, ready)
	private := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, token.Protect(verifier, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if err := sessions.Validate(req.Context(), token.Authorization(req.Context()), token.Subject(req.Context())); err != nil {
				r.fail(w, req, err)
				return
			}
			h(w, req)
		})))
	}
	private("GET /strategies", r.list)
	private("POST /strategies", r.create)
	private("GET /strategies/{strategyId}", r.get)
	private("PUT /strategies/{strategyId}", r.save)
	private("DELETE /strategies/{strategyId}", r.delete)
	private("POST /strategies/import", r.importSource)
	private("GET /strategies/{strategyId}/versions", r.versions)
	private("POST /strategies/{strategyId}/restore", r.restore)
	private("POST /strategies/lint", r.lint)
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
		httpx.Error(w, 404, "not_found", "strategy or version not found")
	case errors.Is(err, domain.ErrConflict):
		httpx.Error(w, 409, "version_conflict", "strategy changed; reload the current version before saving")
	case errors.Is(err, domain.ErrUnauthorized), errors.Is(err, session.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpx.Error(w, 401, "unauthorized", "active session required")
	case errors.Is(err, session.ErrUnavailable):
		httpx.Error(w, 503, "auth_unavailable", "session authority unavailable")
	case errors.Is(err, domain.ErrUnavailable):
		httpx.Error(w, 503, "analysis_unavailable", "static analysis unavailable")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		httpx.Error(w, 503, "unavailable", "request could not complete")
	default:
		r.logger.Error("Strategy request failed", "request_id", httpx.RequestID(req.Context()), "error", err)
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
func pagination(req *http.Request) (string, int, error) {
	q, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return "", 0, domain.ErrInvalid
	}
	for k, v := range q {
		if len(v) != 1 || (k != "limit" && k != "cursor") {
			return "", 0, domain.ErrInvalid
		}
	}
	limit := 0
	if v, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(v[0])
		if err != nil || limit < 1 {
			return "", 0, domain.ErrInvalid
		}
	}
	return q.Get("cursor"), limit, nil
}
func (r *router) list(w http.ResponseWriter, req *http.Request) {
	cursor, limit, err := pagination(req)
	if err != nil {
		r.fail(w, req, err)
		return
	}
	p, err := r.svc.List(req.Context(), uid(req), cursor, limit)
	r.respond(w, req, 200, p, err)
}
func (r *router) create(w http.ResponseWriter, req *http.Request) {
	var in domain.Input
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	s, err := r.svc.Create(req.Context(), uid(req), in)
	r.respond(w, req, 201, s, err)
}
func (r *router) get(w http.ResponseWriter, req *http.Request) {
	s, err := r.svc.Get(req.Context(), uid(req), req.PathValue("strategyId"))
	r.respond(w, req, 200, s, err)
}
func (r *router) save(w http.ResponseWriter, req *http.Request) {
	var in domain.SaveInput
	if !httpx.DecodeLimit(w, req, &in, r.cfg.MaxBodyBytes) {
		return
	}
	s, err := r.svc.Save(req.Context(), uid(req), req.PathValue("strategyId"), in)
	r.respond(w, req, 200, s, err)
}
func (r *router) delete(w http.ResponseWriter, req *http.Request) {
	r.respond(w, req, 204, nil, r.svc.Delete(req.Context(), uid(req), req.PathValue("strategyId")))
}
func (r *router) versions(w http.ResponseWriter, req *http.Request) {
	cursor, limit, err := pagination(req)
	if err != nil {
		r.fail(w, req, err)
		return
	}
	p, err := r.svc.Versions(req.Context(), uid(req), req.PathValue("strategyId"), cursor, limit)
	r.respond(w, req, 200, p, err)
}
func (r *router) restore(w http.ResponseWriter, req *http.Request) {
	var in domain.RestoreInput
	if !httpx.Decode(w, req, &in) {
		return
	}
	s, err := r.svc.Restore(req.Context(), uid(req), req.PathValue("strategyId"), in)
	r.respond(w, req, 200, s, err)
}
func (r *router) lint(w http.ResponseWriter, req *http.Request) {
	var wire struct {
		Source   *string `json:"source"`
		Language string  `json:"language,omitempty"`
	}
	if !httpx.DecodeLimit(w, req, &wire, r.cfg.MaxBodyBytes) {
		return
	}
	if wire.Source == nil {
		r.fail(w, req, domain.ErrInvalid)
		return
	}
	in := domain.LintInput{Source: *wire.Source, Language: wire.Language}
	d, err := r.svc.Lint(req.Context(), uid(req), in)
	r.respond(w, req, 200, d, err)
}

// Stream multipart data in memory only; no ParseMultipartForm temporary files.
func (r *router) importSource(w http.ResponseWriter, req *http.Request) {
	media, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" {
		httpx.Error(w, 415, "unsupported_media_type", "multipart/form-data required")
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, r.cfg.MaxBodyBytes)
	defer req.Body.Close()
	reader, err := req.MultipartReader()
	if err != nil {
		httpx.Error(w, 400, "invalid_multipart", "valid multipart form required")
		return
	}
	bad := func(err error) {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			httpx.Error(w, 413, "body_too_large", "multipart body exceeds the configured limit")
		} else {
			httpx.Error(w, 400, "invalid_multipart", "one name field and one UTF-8 .py file required")
		}
	}
	seen := map[string]bool{}
	var in domain.Input
	for {
		part, e := reader.NextRawPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			bad(e)
			return
		}
		_, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		key := part.FormName()
		if e != nil || seen[key] || (key != "file" && key != "name") || part.Header.Get("Content-Transfer-Encoding") != "" {
			part.Close()
			bad(domain.ErrInvalid)
			return
		}
		seen[key] = true
		cap := 601
		if key == "file" {
			filename, ok := params["filename"]
			if !ok || !strings.HasSuffix(filename, ".py") || strings.ContainsAny(filename, "/\\\x00") || !utf8.ValidString(filename) || len(filename) > 255 {
				part.Close()
				bad(domain.ErrInvalid)
				return
			}
			cap = r.cfg.MaxSourceBytes + 1
		} else {
			if _, ok := params["filename"]; ok {
				part.Close()
				bad(domain.ErrInvalid)
				return
			}
		}
		data, e := io.ReadAll(io.LimitReader(part, int64(cap)))
		if e != nil {
			bad(e)
			return
		}
		if len(data) >= cap {
			httpx.Error(w, 413, "body_too_large", "multipart field exceeds the configured limit")
			return
		}
		part.Close()
		if key == "file" {
			in.Source = strings.TrimPrefix(string(data), "\uFEFF")
		} else {
			in.Name = string(data)
		}
	}
	if !seen["file"] || !seen["name"] {
		bad(domain.ErrInvalid)
		return
	}
	// Consume the epilogue too, so padding cannot bypass the whole-body cap.
	if _, err = io.Copy(io.Discard, req.Body); err != nil {
		bad(err)
		return
	}
	s, err := r.svc.Create(req.Context(), uid(req), in)
	r.respond(w, req, 201, s, err)
}
