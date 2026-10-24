package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"qslate/gateway/internal/config"
	"qslate/gateway/internal/middleware"
	"qslate/internal/platform/httpx"
	"qslate/internal/platform/token"
)

func New(routes []config.Route, verifier token.Verifier, logger *slog.Logger) (http.Handler, error) {
	mux := http.NewServeMux()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.MaxConnsPerHost = 100
	limiter := middleware.NewLimiter(10, time.Minute)
	seen := map[string]bool{}
	upstreams := map[string]bool{}
	for _, route := range routes {
		if err := config.Validate(route); err != nil {
			return nil, err
		}
		pattern := route.Method + " " + route.Path
		if seen[pattern] {
			return nil, fmt.Errorf("duplicate route %s", pattern)
		}
		seen[pattern] = true
		if route.AuthRequired && verifier == nil {
			return nil, fmt.Errorf("protected route requires a verifier")
		}
		target, _ := url.Parse(route.Upstream)
		upstreams[route.Upstream] = true
		proxy := &httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(request *httputil.ProxyRequest) {
				request.SetURL(target)
				path := route.TargetPath
				names, _ := config.Parameters(route.Path)
				for _, name := range names {
					path = strings.ReplaceAll(path, "{"+name+"}", request.In.PathValue(name))
				}
				request.Out.URL.Path = path
				request.Out.URL.RawPath = ""
				// Remove supplied identities and forwarding headers before setting trusted values.
				for name := range request.Out.Header {
					lower := strings.ToLower(name)
					if lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-user-") || strings.HasPrefix(lower, "x-internal-") || lower == "x-real-ip" {
						request.Out.Header.Del(name)
					}
				}
				request.SetXForwarded()
				request.Out.Header.Set("X-Request-ID", httpx.RequestID(request.In.Context()))
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				logger.Warn("upstream failure", "error", err, "request_id", httpx.RequestID(r.Context()))
				status := 502
				var networkError net.Error
				if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
					status = 504
				}
				httpx.Error(w, status, "upstream_unavailable", "upstream service unavailable")
			},
		}
		var handler http.Handler = proxy
		if route.AuthRequired {
			handler = token.Protect(verifier, handler)
		}
		if route.RateLimited {
			handler = limiter.Wrap(handler)
		}
		// Trailing slash patterns in ServeMux match subtrees; ensure only declared paths pass.
		wrapped := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			names, _ := config.Parameters(route.Path)
			for _, name := range names {
				value := r.PathValue(name)
				if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") {
					httpx.Error(w, 400, "invalid_path_parameter", "invalid resource identifier")
					return
				}
			}
			if r.Method != route.Method {
				w.Header().Set("Allow", route.Method)
				httpx.Error(w, 405, "method_not_allowed", "method not allowed")
				return
			}
			wrapped.ServeHTTP(w, r)
		})
		mux.Handle(pattern, handler)
	}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	httpx.Probes(mux, func(ctx context.Context) error {
		for origin := range upstreams {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/readyz", nil)
			if err != nil {
				return err
			}
			response, err := client.Do(req)
			if err != nil {
				return err
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				return errors.New("upstream not ready")
			}
		}
		return nil
	})
	return mux, nil
}
