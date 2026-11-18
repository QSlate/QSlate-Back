// Package feed reads only an explicitly configured trusted source; it generates no prices.
package feed

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"os"
	"qslate/services/market-data/internal/domain"
	"qslate/services/market-data/internal/service"
	"regexp"
	"time"
)

type Client struct {
	cfg            service.LiveConfig
	origin, secret string
	http           *http.Client
}

func New(c service.LiveConfig) (*Client, error) {
	v := &Client{cfg: c, http: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if !c.Enabled {
		return v, nil
	}
	s, e := service.Origin(os.Getenv(c.OriginEnv), c.AllowHTTP)
	if e != nil {
		return nil, e
	}
	v.origin = s
	if c.TokenEnv != "" {
		v.secret = os.Getenv(c.TokenEnv)
	}
	return v, nil
}
func (c *Client) Allowed(owner int64, symbol string) bool {
	if c == nil || !c.cfg.Enabled {
		return false
	}
	for _, s := range c.cfg.Symbols {
		if s.Symbol == symbol && (s.OwnerID == 0 || s.OwnerID == owner) {
			return true
		}
	}
	return false
}
func (c *Client) Symbols(owner int64) []domain.Symbol {
	out := []domain.Symbol{}
	if c == nil || !c.cfg.Enabled {
		return out
	}
	for _, s := range c.cfg.Symbols {
		if s.OwnerID == 0 || s.OwnerID == owner {
			out = append(out, domain.Symbol{Symbol: s.Symbol, Name: s.Name, Live: true})
		}
	}
	return out
}
func (c *Client) request(ctx context.Context, path string, in, out any) error {
	if c == nil || !c.cfg.Enabled {
		return domain.ErrUnavailable
	}
	b, _ := json.Marshal(in)
	req, e := http.NewRequestWithContext(ctx, "POST", c.origin+path, bytes.NewReader(b))
	if e != nil {
		return domain.ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return domain.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return domain.ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if e != nil || len(raw) > 1<<20 {
		return domain.ErrUnavailable
	}
	if json.Unmarshal(raw, out) != nil {
		return domain.ErrUnavailable
	}
	return nil
}

var amount = regexp.MustCompile(`^[0-9]{1,20}(\.[0-9]{1,18})?$`)

func validAmount(s string, positive bool) bool {
	if !amount.MatchString(s) {
		return false
	}
	v, ok := new(big.Rat).SetString(s)
	return ok && (v.Sign() > 0 || (!positive && v.Sign() == 0))
}
func (c *Client) Ticks(ctx context.Context, owner int64, symbols []string, after map[string]string) ([]domain.Tick, error) {
	for _, s := range symbols {
		if !c.Allowed(owner, s) {
			return nil, domain.ErrNotFound
		}
	}
	var out struct {
		Items []domain.Tick `json:"items"`
	}
	if e := c.request(ctx, "/ticks", map[string]any{"symbols": symbols, "after": after}, &out); e != nil {
		return nil, e
	}
	if len(out.Items) > 1000 {
		return nil, domain.ErrUnavailable
	}
	set := map[string]bool{}
	for _, s := range symbols {
		set[s] = true
	}
	for _, v := range out.Items {
		if !set[v.Symbol] || v.ID == "" || len(v.ID) > 128 || v.Time.IsZero() || v.Time.After(time.Now().Add(time.Minute)) || !validAmount(v.Price, true) || !validAmount(v.Quantity, false) {
			return nil, domain.ErrUnavailable
		}
	}
	return out.Items, nil
}
func (c *Client) History(ctx context.Context, owner int64, symbol, resolution string, start, end time.Time, cursor string, limit int) (domain.History, error) {
	out := domain.History{Symbol: symbol, Resolution: resolution}
	if !c.Allowed(owner, symbol) {
		return out, domain.ErrNotFound
	}
	if e := c.request(ctx, "/history", map[string]any{"symbol": symbol, "resolution": resolution, "start": start, "end": end, "cursor": cursor, "limit": limit}, &out); e != nil {
		return out, e
	}
	if out.Symbol != symbol || out.Resolution != resolution || len(out.Items) > limit {
		return out, domain.ErrUnavailable
	}
	prev := start.Add(-time.Nanosecond)
	for _, v := range out.Items {
		if !v.Time.After(prev) || v.Time.Before(start) || !v.Time.Before(end) || !validAmount(v.Open, true) || !validAmount(v.High, true) || !validAmount(v.Low, true) || !validAmount(v.Close, true) || !validAmount(v.Volume, false) {
			return out, domain.ErrUnavailable
		}
		prev = v.Time
	}
	if out.Items == nil {
		out.Items = []domain.Candle{}
	}
	return out, nil
}
