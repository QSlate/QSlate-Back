package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"qslate/gateway/internal/config"
	"qslate/gateway/internal/middleware"
	"qslate/gateway/internal/platform/httpx"
	"testing"
	"time"
)

func TestWebSocketUpgradeTunnelsBeyondHTTPDeadline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, e := http.NewResponseController(w).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		defer conn.Close()
		if r.URL.Path != "/ws/backtests/run-id/stream" || r.Header.Get("X-User-ID") != "" {
			t.Error("invalid upgrade route or spoofed identity")
		}
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		// The proxy must tunnel opaque upgraded bytes, without consuming WS frames.
		_, _ = rw.WriteString("engine-progress")
		_ = rw.Flush()
	}))
	defer upstream.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, e := New([]config.Route{{Method: "GET", Path: "/qslate/v1/ws/backtests/{backtestId}/stream", TargetPath: "/ws/backtests/{backtestId}/stream", Upstream: upstream.URL}}, nil, logger)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(httpx.Middleware(logger, h))
	server.Config.WriteTimeout = 20 * time.Millisecond
	server.Config.ReadTimeout = 20 * time.Millisecond
	server.Start()
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/qslate/v1/ws/backtests/run-id/stream", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("X-User-ID", "forged")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 101 {
		t.Fatal(resp.StatusCode)
	}
	body, e := io.ReadAll(resp.Body)
	if e != nil || string(body) != "engine-progress" {
		t.Fatalf("upgrade tunnel: %q %v", body, e)
	}
}
func TestCORSAllowsLabIdempotencyHeader(t *testing.T) {
	h := middleware.CORS("http://localhost:3000", http.NotFoundHandler())
	req := httptest.NewRequest("OPTIONS", "/qslate/v1/backtests/run", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Idempotency-Key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type, Idempotency-Key" {
		t.Fatal("browser cannot submit idempotent runs")
	}
}
