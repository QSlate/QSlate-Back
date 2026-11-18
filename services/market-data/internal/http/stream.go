package http

import (
	"context"
	"github.com/gorilla/websocket"
	"net/http"
	"qslate/services/market-data/internal/domain"
	"qslate/services/market-data/internal/platform/httpx"
	"sort"
	"time"
)

type command struct {
	Type        string   `json:"type"`
	AccessToken string   `json:"access_token,omitempty"`
	Symbols     []string `json:"symbols,omitempty"`
}

func (r *router) stream(w http.ResponseWriter, req *http.Request) {
	if req.URL.RawQuery != "" {
		r.fail(w, domain.ErrInvalid)
		return
	}
	select {
	case r.streams <- struct{}{}:
		defer func() { <-r.streams }()
	default:
		httpx.Error(w, 503, "stream_capacity", "stream limit reached")
		return
	}
	u := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(req *http.Request) bool {
		if req.Header.Get("Origin") == "" {
			return true
		}
		for _, o := range r.svc.Config.AllowedOrigins {
			if o == req.Header.Get("Origin") {
				return true
			}
		}
		return false
	}}
	c, e := u.Upgrade(w, req, nil)
	if e != nil {
		return
	}
	defer c.Close()
	closeWith := func(code int, reason string) {
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	}
	write := func(v any) error { _ = c.SetWriteDeadline(time.Now().Add(5 * time.Second)); return c.WriteJSON(v) }
	c.SetReadLimit(16384)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth command
	if e = c.ReadJSON(&auth); e != nil || auth.Type != "authenticate" {
		closeWith(1008, "authentication required")
		return
	}
	owner, e := r.verifier.Verify(auth.AccessToken)
	if e != nil {
		closeWith(1008, "invalid token")
		return
	}
	check := func() error {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		id, e := r.verifier.Verify(auth.AccessToken)
		if e != nil || id != owner {
			return domain.ErrUnauthorized
		}
		return r.sessions.Validate(ctx, "Bearer "+auth.AccessToken, owner)
	}
	if check() != nil {
		closeWith(1008, "active session required")
		return
	}
	if e = write(map[string]any{"type": "authenticated"}); e != nil {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(30 * time.Second)) })
	incoming := make(chan command, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var cmd command
			if c.ReadJSON(&cmd) != nil {
				return
			}
			select {
			case incoming <- cmd:
			default:
				_ = c.Close()
				return
			}
		}
	}()
	subscribed := map[string]bool{}
	after := map[string]string{}
	lastTime := map[string]time.Time{}
	sequence := 0
	poll := time.NewTicker(time.Duration(r.svc.Config.Live.PollSeconds) * time.Second)
	defer poll.Stop()
	sessionTick := time.NewTicker(time.Second)
	defer sessionTick.Stop()
	ping := time.NewTicker(10 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-done:
			return
		case <-sessionTick.C:
			if check() != nil {
				closeWith(1008, "session expired or revoked")
				return
			}
		case <-ping.C:
			if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)) != nil {
				return
			}
		case cmd := <-incoming:
			if check() != nil {
				closeWith(1008, "active session required")
				return
			}
			switch cmd.Type {
			case "subscribe", "unsubscribe":
				if len(cmd.Symbols) == 0 || len(cmd.Symbols) > r.svc.Config.Live.MaxSymbols {
					closeWith(1008, "subscription limit")
					return
				}
				for _, s := range cmd.Symbols {
					if cmd.Type == "subscribe" {
						if !r.feeds.Allowed(owner, s) {
							closeWith(1008, "symbol unavailable or no live source configured")
							return
						}
						subscribed[s] = true
					} else {
						delete(subscribed, s)
						delete(after, s)
						delete(lastTime, s)
					}
				}
				if len(subscribed) > r.svc.Config.Live.MaxSymbols {
					closeWith(1008, "subscription limit")
					return
				}
				if write(map[string]any{"type": "subscriptions", "symbols": sorted(subscribed)}) != nil {
					return
				}
			case "ping":
				if write(map[string]any{"type": "pong", "time": time.Now().UTC()}) != nil {
					return
				}
			default:
				closeWith(1008, "unsupported command")
				return
			}
		case <-poll.C:
			if len(subscribed) == 0 {
				continue
			}
			if check() != nil {
				closeWith(1008, "active session required")
				return
			}
			ticks, e := r.feeds.Ticks(req.Context(), owner, sorted(subscribed), after)
			if e != nil {
				closeWith(1013, "live data source unavailable")
				return
			}
			for _, v := range ticks {
				if v.ID == after[v.Symbol] {
					continue
				}
				if !lastTime[v.Symbol].IsZero() && v.Time.Before(lastTime[v.Symbol]) {
					closeWith(1013, "out-of-order live feed")
					return
				}
				sequence++
				if write(map[string]any{"type": "market.tick.v1", "schema_version": 1, "event_id": v.Symbol + ":" + v.ID, "sequence": sequence, "time": v.Time, "symbol": v.Symbol, "price": v.Price, "quantity": v.Quantity}) != nil {
					return
				}
				after[v.Symbol] = v.ID
				lastTime[v.Symbol] = v.Time
			}
		}
	}
}
func sorted(m map[string]bool) []string {
	a := make([]string, 0, len(m))
	for s := range m {
		a = append(a, s)
	}
	sort.Strings(a)
	return a
}
