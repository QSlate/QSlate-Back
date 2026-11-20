package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"qslate/services/market-data/internal/domain"
	"qslate/services/market-data/internal/feed"
	"qslate/services/market-data/internal/platform/httpx"
	"qslate/services/market-data/internal/platform/session"
	"qslate/services/market-data/internal/platform/token"
	"qslate/services/market-data/internal/service"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Sessions interface {
	Validate(context.Context, string, int64) error
}
type router struct {
	svc      *service.Data
	feeds    *feed.Client
	verifier token.Verifier
	sessions Sessions
	logger   *slog.Logger
	streams  chan struct{}
}

func New(svc *service.Data, feeds *feed.Client, verifier token.Verifier, sessions Sessions, ready func(context.Context) error, logger *slog.Logger) http.Handler {
	r := &router{svc, feeds, verifier, sessions, logger, make(chan struct{}, svc.Config.Live.MaxStreams)}
	mux := http.NewServeMux()
	httpx.Probes(mux, ready)
	private := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, token.Protect(verifier, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx, cancel := context.WithTimeout(req.Context(), 12*time.Second)
			defer cancel()
			req = req.WithContext(ctx)
			if e := sessions.Validate(ctx, token.Authorization(ctx), token.Subject(ctx)); e != nil {
				r.fail(w, e)
				return
			}
			if id := req.PathValue("datasetId"); id != "" && !domain.ValidID(id) {
				r.fail(w, domain.ErrInvalid)
				return
			}
			h(w, req)
		})))
	}
	private("POST /data/custom/upload", r.upload)
	private("GET /data/custom", r.list)
	private("GET /data/custom/{datasetId}", r.get)
	private("DELETE /data/custom/{datasetId}", r.delete)
	private("POST /data/custom/{datasetId}/mapping", r.mapping)
	private("POST /data/custom/{datasetId}/validate", r.validate)
	private("GET /data/symbols", r.symbols)
	private("GET /data/history", r.history)
	private("POST /catalogue/dataset-snapshot", r.snapshot)
	mux.HandleFunc("GET /ws/data/ticks", r.stream)
	return mux
}
func (r *router) fail(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, domain.ErrInvalid):
		httpx.Error(w, 400, "invalid_input", "invalid dataset, mapping or query")
	case errors.Is(e, domain.ErrNotFound):
		httpx.Error(w, 404, "not_found", "dataset, version or symbol unavailable")
	case errors.Is(e, domain.ErrConflict):
		httpx.Error(w, 409, "state_conflict", "version changed, mapping required or dataset not ready")
	case errors.Is(e, domain.ErrQuota):
		httpx.Error(w, 429, "quota_exceeded", "dataset storage/version limit reached")
	case errors.Is(e, domain.ErrUnauthorized), errors.Is(e, session.ErrUnauthorized):
		httpx.Error(w, 401, "unauthorized", "active session required")
	case errors.Is(e, domain.ErrUnavailable), errors.Is(e, session.ErrUnavailable), errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		httpx.Error(w, 503, "data_unavailable", "data source, parser or session authority unavailable")
	default:
		r.logger.Error("Market Data request failed", "error", e)
		httpx.Error(w, 500, "internal_error", "internal server error")
	}
}
func (r *router) respond(w http.ResponseWriter, code int, value any, e error) {
	if e != nil {
		r.fail(w, e)
		return
	}
	if code == 204 {
		w.WriteHeader(code)
		return
	}
	httpx.JSON(w, code, value)
}
func uid(req *http.Request) int64 { return token.Subject(req.Context()) }
func (r *router) get(w http.ResponseWriter, req *http.Request) {
	d, e := r.svc.Repo.Get(req.Context(), uid(req), req.PathValue("datasetId"))
	r.respond(w, 200, d, e)
}
func (r *router) delete(w http.ResponseWriter, req *http.Request) {
	r.respond(w, 204, nil, r.svc.Repo.Delete(req.Context(), uid(req), req.PathValue("datasetId")))
}
func (r *router) mapping(w http.ResponseWriter, req *http.Request) {
	var in struct {
		domain.Mapping
		BaseVersionID string `json:"base_version_id"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	d, e := r.svc.Map(req.Context(), uid(req), req.PathValue("datasetId"), in.BaseVersionID, in.Mapping)
	r.respond(w, 200, d, e)
}
func (r *router) validate(w http.ResponseWriter, req *http.Request) {
	d, e := r.svc.Validate(req.Context(), uid(req), req.PathValue("datasetId"), req.Header.Get("Idempotency-Key"))
	r.respond(w, 202, d, e)
}
func (r *router) snapshot(w http.ResponseWriter, req *http.Request) {
	var in struct {
		DatasetID string `json:"dataset_id"`
		VersionID string `json:"version_id"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	if !domain.ValidID(in.DatasetID) || !domain.ValidID(in.VersionID) {
		r.fail(w, domain.ErrInvalid)
		return
	}
	d, e := r.svc.Repo.Snapshot(req.Context(), uid(req), in.DatasetID, in.VersionID)
	r.respond(w, 200, d, e)
}
func query(req *http.Request, allowed ...string) (url.Values, error) {
	q, e := url.ParseQuery(req.URL.RawQuery)
	if e != nil {
		return nil, domain.ErrInvalid
	}
	set := map[string]bool{}
	for _, k := range allowed {
		set[k] = true
	}
	for k, v := range q {
		if len(v) != 1 || !set[k] {
			return nil, domain.ErrInvalid
		}
	}
	return q, nil
}
func (r *router) page(q url.Values) (int, error) {
	limit := 100
	if limit > r.svc.Config.MaxPageSize {
		limit = r.svc.Config.MaxPageSize
	}
	if _, ok := q["limit"]; ok {
		n, e := strconv.Atoi(q.Get("limit"))
		if e != nil || n < 1 || n > r.svc.Config.MaxPageSize {
			return 0, domain.ErrInvalid
		}
		limit = n
	}
	return limit, nil
}
func (r *router) list(w http.ResponseWriter, req *http.Request) {
	q, e := query(req, "limit", "cursor")
	if e != nil {
		r.fail(w, e)
		return
	}
	n, e := r.page(q)
	if e != nil || (q.Get("cursor") != "" && !domain.ValidID(q.Get("cursor"))) {
		r.fail(w, domain.ErrInvalid)
		return
	}
	out, e := r.svc.Repo.List(req.Context(), uid(req), q.Get("cursor"), n)
	for i := range out.Items {
		out.Items[i].Preview = nil
		out.Items[i].Mapping = nil
	}
	r.respond(w, 200, out, e)
}
func (r *router) symbols(w http.ResponseWriter, req *http.Request) {
	q, e := query(req, "limit", "cursor", "q")
	if e != nil || len(q.Get("q")) > 200 {
		r.fail(w, domain.ErrInvalid)
		return
	}
	n, e := r.page(q)
	if e != nil {
		r.fail(w, e)
		return
	}
	items, e := r.svc.Repo.Symbols(req.Context(), uid(req))
	if e != nil {
		r.fail(w, e)
		return
	}
	items = append(items, r.feeds.Symbols(uid(req))...)
	sort.Slice(items, func(i, j int) bool { return items[i].Symbol < items[j].Symbol })
	out := []domain.Symbol{}
	for _, s := range items {
		if s.Symbol > q.Get("cursor") && strings.Contains(strings.ToLower(s.Symbol+" "+s.Name), strings.ToLower(q.Get("q"))) {
			out = append(out, s)
		}
	}
	var next any
	if len(out) > n {
		out = out[:n]
		next = out[n-1].Symbol
	}
	httpx.JSON(w, 200, map[string]any{"items": out, "next_cursor": next})
}
func (r *router) history(w http.ResponseWriter, req *http.Request) {
	q, e := query(req, "symbol", "start", "end", "resolution", "cursor", "limit", "version_id")
	if e != nil {
		r.fail(w, e)
		return
	}
	n, e := r.page(q)
	if e != nil {
		r.fail(w, e)
		return
	}
	start, e1 := time.Parse(time.RFC3339Nano, q.Get("start"))
	end, e2 := time.Parse(time.RFC3339Nano, q.Get("end"))
	resolution := q.Get("resolution")
	seconds, ok := service.Resolutions[resolution]
	if e1 != nil || e2 != nil || !end.After(start) || end.Sub(start) > 366*24*time.Hour || !ok {
		r.fail(w, domain.ErrInvalid)
		return
	}
	var cursor *time.Time
	if q.Get("cursor") != "" {
		t, e := time.Parse(time.RFC3339Nano, q.Get("cursor"))
		if e != nil || t.Before(start.Truncate(24*time.Hour)) || !t.Before(end) {
			r.fail(w, domain.ErrInvalid)
			return
		}
		cursor = &t
	}
	if !strings.HasPrefix(q.Get("symbol"), "custom:") {
		h, e := r.feeds.History(req.Context(), uid(req), q.Get("symbol"), resolution, start, end, q.Get("cursor"), n)
		r.respond(w, 200, h, e)
		return
	}
	if v := q.Get("version_id"); v != "" && !domain.ValidID(v) {
		r.fail(w, domain.ErrInvalid)
		return
	}
	out, e := r.svc.Repo.History(req.Context(), uid(req), q.Get("symbol"), q.Get("version_id"), resolution, start, end, seconds, n, cursor)
	r.respond(w, 200, out, e)
}
func (r *router) upload(w http.ResponseWriter, req *http.Request) {
	media, _, e := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if e != nil || media != "multipart/form-data" {
		httpx.Error(w, 415, "unsupported_media_type", "multipart/form-data required")
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, r.svc.Config.MaxUploadBytes+16384)
	defer req.Body.Close()
	reader, e := req.MultipartReader()
	if e != nil {
		r.fail(w, domain.ErrInvalid)
		return
	}
	var name, format string
	var raw []byte
	seen := map[string]bool{}
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(e, &tooLarge) {
				httpx.Error(w, 413, "upload_too_large", "upload limit exceeded")
			} else {
				r.fail(w, domain.ErrInvalid)
			}
			return
		}
		field := part.FormName()
		if seen[field] || (field != "name" && field != "file") || part.Header.Get("Content-Transfer-Encoding") != "" {
			r.fail(w, domain.ErrInvalid)
			return
		}
		seen[field] = true
		if field == "name" {
			b, e := io.ReadAll(io.LimitReader(part, 601))
			if e != nil || len(b) > 600 || part.FileName() != "" {
				r.fail(w, domain.ErrInvalid)
				return
			}
			name = string(b)
		} else {
			_, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			filename := params["filename"]
			if e != nil || filename == "" || len(filename) > 255 || strings.ContainsAny(filename, "/\\\x00") {
				r.fail(w, domain.ErrInvalid)
				return
			}
			format = strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
			if format != "csv" && format != "parquet" {
				httpx.Error(w, 415, "unsupported_format", "CSV or Parquet required")
				return
			}
			kind := part.Header.Get("Content-Type")
			if kind != "" && kind != "application/octet-stream" && !(format == "csv" && (kind == "text/csv" || kind == "text/plain")) && !(format == "parquet" && (kind == "application/vnd.apache.parquet" || kind == "application/x-parquet")) {
				httpx.Error(w, 415, "unsupported_media_type", "file MIME type does not match format")
				return
			}
			raw, e = io.ReadAll(io.LimitReader(part, r.svc.Config.MaxUploadBytes+1))
			if e != nil || int64(len(raw)) > r.svc.Config.MaxUploadBytes {
				httpx.Error(w, 413, "upload_too_large", "upload limit exceeded")
				return
			}
		}
		_ = part.Close()
	}
	if !seen["name"] || !seen["file"] {
		r.fail(w, domain.ErrInvalid)
		return
	}
	out, e := r.svc.Upload(req.Context(), uid(req), name, format, raw)
	r.respond(w, 201, out, e)
}
