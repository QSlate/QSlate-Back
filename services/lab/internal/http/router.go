package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/platform/httpx"
	"qslate/services/lab/internal/platform/session"
	"qslate/services/lab/internal/platform/token"
	"qslate/services/lab/internal/service"
	"strconv"
	"strings"
	"time"
)

type Sessions interface {
	Validate(context.Context, string, int64) error
}
type router struct {
	svc      *service.Lab
	verifier token.Verifier
	sessions Sessions
	logger   *slog.Logger
	streams  chan struct{}
}

func New(svc *service.Lab, verifier token.Verifier, sessions Sessions, ready func(context.Context) error, logger *slog.Logger) http.Handler {
	r := &router{svc, verifier, sessions, logger, make(chan struct{}, 128)}
	mux := http.NewServeMux()
	httpx.Probes(mux, ready)
	private := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, token.Protect(verifier, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
			defer cancel()
			req = req.WithContext(ctx)
			if e := sessions.Validate(ctx, token.Authorization(ctx), token.Subject(ctx)); e != nil {
				r.fail(w, e)
				return
			}
			if id := req.PathValue("backtestId"); id != "" && !domain.ValidID(id) {
				r.fail(w, domain.ErrInvalid)
				return
			}
			h(w, req)
		})))
	}
	private("POST /backtests/run", r.run)
	private("POST /backtests/optimize", r.optimize)
	private("POST /backtests/compare", r.compare)
	private("GET /backtests", r.list)
	private("GET /backtests/{backtestId}/status", r.status)
	private("POST /backtests/{backtestId}/cancel", r.cancel)
	private("DELETE /backtests/{backtestId}", r.delete)
	private("GET /backtests/{backtestId}/summary", r.summary)
	for _, kind := range []string{"trades", "equity-curve", "logs"} {
		private("GET /backtests/{backtestId}/"+kind, func(w http.ResponseWriter, req *http.Request) { r.artifact(w, req, kind) })
	}
	mux.HandleFunc("GET /ws/backtests/{backtestId}/stream", r.stream)
	return mux
}
func (r *router) fail(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, domain.ErrInvalid):
		httpx.Error(w, 400, "invalid_input", "invalid backtest input, cursor or idempotency key")
	case errors.Is(e, domain.ErrNotFound):
		httpx.Error(w, 404, "not_found", "backtest or pinned resource not found")
	case errors.Is(e, domain.ErrConflict):
		httpx.Error(w, 409, "state_conflict", "result not ready, cleanup pending or idempotency key reused")
	case errors.Is(e, domain.ErrQuota):
		httpx.Error(w, 429, "quota_exceeded", "outstanding backtest limit reached")
	case errors.Is(e, domain.ErrUnauthorized), errors.Is(e, session.ErrUnauthorized):
		httpx.Error(w, 401, "unauthorized", "active session required")
	case errors.Is(e, domain.ErrUnavailable), errors.Is(e, session.ErrUnavailable), errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		httpx.Error(w, 503, "execution_unavailable", "execution, session authority or pinned data unavailable")
	default:
		r.logger.Error("Lab request failed", "error", e)
		httpx.Error(w, 500, "internal_error", "internal server error")
	}
}
func (r *router) respond(w http.ResponseWriter, code int, out any, e error) {
	if e != nil {
		r.fail(w, e)
		return
	}
	if code == 204 {
		w.WriteHeader(204)
		return
	}
	httpx.JSON(w, code, out)
}
func owner(req *http.Request) int64 { return token.Subject(req.Context()) }
func (r *router) run(w http.ResponseWriter, req *http.Request) {
	var in domain.RunInput
	if !httpx.Decode(w, req, &in) {
		return
	}
	out, e := r.svc.Run(req.Context(), owner(req), req.Header.Get("Idempotency-Key"), in)
	r.respond(w, 202, out, e)
}
func (r *router) optimize(w http.ResponseWriter, req *http.Request) {
	var in domain.OptimizeInput
	if !httpx.Decode(w, req, &in) {
		return
	}
	out, e := r.svc.Optimize(req.Context(), owner(req), req.Header.Get("Idempotency-Key"), in)
	r.respond(w, 202, out, e)
}
func (r *router) compare(w http.ResponseWriter, req *http.Request) {
	var in struct {
		IDs []string `json:"backtest_ids"`
	}
	if !httpx.Decode(w, req, &in) {
		return
	}
	out, e := r.svc.Compare(req.Context(), owner(req), in.IDs)
	r.respond(w, 200, map[string]any{"summaries": out}, e)
}
func (r *router) status(w http.ResponseWriter, req *http.Request) {
	out, e := r.svc.Repo.Get(req.Context(), owner(req), req.PathValue("backtestId"))
	r.respond(w, 200, out, e)
}
func (r *router) cancel(w http.ResponseWriter, req *http.Request) {
	if !service.ValidKey(req.Header.Get("Idempotency-Key")) {
		r.fail(w, domain.ErrInvalid)
		return
	}
	out, e := r.svc.Repo.Cancel(req.Context(), owner(req), req.PathValue("backtestId"))
	r.respond(w, 202, out, e)
}
func (r *router) delete(w http.ResponseWriter, req *http.Request) {
	e := r.svc.Repo.Delete(req.Context(), owner(req), req.PathValue("backtestId"))
	r.respond(w, 204, nil, e)
}
func (r *router) summary(w http.ResponseWriter, req *http.Request) {
	out, e := r.svc.Summary(req.Context(), owner(req), req.PathValue("backtestId"))
	r.respond(w, 200, out, e)
}
func (r *router) pagination(req *http.Request) (string, int, error) {
	q, e := url.ParseQuery(req.URL.RawQuery)
	if e != nil {
		return "", 0, domain.ErrInvalid
	}
	for k, v := range q {
		if len(v) != 1 || (k != "limit" && k != "cursor") {
			return "", 0, domain.ErrInvalid
		}
	}
	limit := 100
	if limit > r.svc.Config.MaxPageSize {
		limit = r.svc.Config.MaxPageSize
	}
	if v := q.Get("limit"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil || limit < 1 || limit > r.svc.Config.MaxPageSize {
			return "", 0, domain.ErrInvalid
		}
	}
	return q.Get("cursor"), limit, nil
}
func (r *router) list(w http.ResponseWriter, req *http.Request) {
	cursor, limit, e := r.pagination(req)
	if e != nil || (cursor != "" && !domain.ValidID(cursor)) {
		r.fail(w, domain.ErrInvalid)
		return
	}
	out, e := r.svc.Repo.List(req.Context(), owner(req), cursor, limit)
	r.respond(w, 200, out, e)
}
func (r *router) artifact(w http.ResponseWriter, req *http.Request, kind string) {
	cursor, limit, e := r.pagination(req)
	if e != nil {
		r.fail(w, e)
		return
	}
	offset := 0
	if cursor != "" {
		offset, e = strconv.Atoi(cursor)
		if e != nil || offset < 0 || strconv.Itoa(offset) != cursor {
			r.fail(w, domain.ErrInvalid)
			return
		}
	}
	id := req.PathValue("backtestId")
	j, e := r.svc.Repo.Get(req.Context(), owner(req), id)
	if e != nil {
		r.fail(w, e)
		return
	}
	if kind != "logs" && j.Status != "completed" {
		r.fail(w, domain.ErrConflict)
		return
	}
	result, e := r.svc.Repo.Result(req.Context(), owner(req), id)
	if e != nil && !(kind == "logs" && errors.Is(e, domain.ErrConflict)) {
		r.fail(w, e)
		return
	}
	items := []json.RawMessage{}
	switch kind {
	case "trades":
		items = result.Trades
	case "equity-curve":
		peak, _ := strconv.ParseFloat(j.Input.InitialCapital, 64)
		for _, row := range result.Equity {
			var v struct {
				Timestamp string  `json:"timestamp"`
				Equity    float64 `json:"equity"`
			}
			if e = json.Unmarshal(row, &v); e != nil {
				r.fail(w, e)
				return
			}
			if v.Equity > peak {
				peak = v.Equity
			}
			drawdown := 0.0
			if peak > 0 {
				drawdown = (peak - v.Equity) / peak * 100
			}
			raw, _ := json.Marshal(map[string]any{"time": v.Timestamp, "equity": strconv.FormatFloat(v.Equity, 'f', -1, 64), "drawdown": drawdown})
			items = append(items, raw)
		}
	case "logs":
		if result.Logs != "" {
			for _, line := range strings.Split(result.Logs, "\n") {
				raw, _ := json.Marshal(map[string]string{"message": line})
				items = append(items, raw)
			}
		}
	}
	if offset > len(items) {
		r.fail(w, domain.ErrInvalid)
		return
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	var next any
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	httpx.JSON(w, 200, map[string]any{"items": items[offset:end], "next_cursor": next})
}
