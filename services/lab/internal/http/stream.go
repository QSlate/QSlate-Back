package http

import (
	"context"
	"github.com/gorilla/websocket"
	"net/http"
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/platform/httpx"
	"time"
)

func (r *router) stream(w http.ResponseWriter, req *http.Request) {
	if !domain.ValidID(req.PathValue("backtestId")) || req.URL.RawQuery != "" {
		r.fail(w, domain.ErrInvalid)
		return
	}
	select {
	case r.streams <- struct{}{}:
		defer func() { <-r.streams }()
	default:
		httpx.Error(w, 503, "stream_capacity", "stream capacity reached")
		return
	}
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(req *http.Request) bool {
		origin := req.Header.Get("Origin")
		if origin == "" {
			return true
		}
		for _, allowed := range r.svc.Config.AllowedOrigins {
			if origin == allowed {
				return true
			}
		}
		return false
	}}
	conn, e := upgrader.Upgrade(w, req, nil)
	if e != nil {
		return
	}
	defer conn.Close()
	closeWith := func(code int, message string) {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, message), time.Now().Add(time.Second))
	}
	conn.SetReadLimit(8192)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth struct {
		Type        string `json:"type"`
		AccessToken string `json:"access_token"`
		After       int64  `json:"after_event_id"`
	}
	if e = conn.ReadJSON(&auth); e != nil || auth.Type != "authenticate" || auth.After < 0 {
		closeWith(1008, "authentication required")
		return
	}
	uid, e := r.verifier.Verify(auth.AccessToken)
	if e != nil || uid <= 0 {
		closeWith(1008, "invalid access token")
		return
	}
	check := func() error {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		id, e := r.verifier.Verify(auth.AccessToken)
		if e != nil || id != uid {
			return domain.ErrUnauthorized
		}
		return r.sessions.Validate(ctx, "Bearer "+auth.AccessToken, uid)
	}
	if e = check(); e != nil {
		closeWith(1008, "active session required")
		return
	}
	id := req.PathValue("backtestId")
	if _, e = r.svc.Repo.Get(req.Context(), uid, id); e != nil {
		closeWith(1008, "backtest unavailable")
		return
	}
	// Only the reader goroutine changes read deadlines after authentication.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(30 * time.Second)) })
	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		for {
			if _, _, e := conn.ReadMessage(); e != nil {
				return
			}
			closeWith(1008, "unexpected application message")
			_ = conn.Close()
			return
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	ping := time.NewTicker(10 * time.Second)
	defer ping.Stop()
	after := auth.After
	for {
		if e = check(); e != nil {
			closeWith(1008, "active session required")
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		events, err := r.svc.Repo.Events(ctx, uid, id, after)
		j, jobErr := r.svc.Repo.Get(ctx, uid, id)
		cancel()
		if err != nil || jobErr != nil {
			closeWith(1013, "backtest unavailable")
			return
		}
		for _, event := range events {
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if e = conn.WriteJSON(event); e != nil {
				return
			}
			after = event.ID
		}
		if j.Terminal() && len(events) < 100 {
			closeWith(1000, "backtest terminal")
			return
		}
		select {
		case <-req.Context().Done():
			return
		case <-disconnected:
			return
		case <-ping.C:
			if e = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); e != nil {
				return
			}
		case <-tick.C:
		}
	}
}
