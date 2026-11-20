// Package strategy resolves an authorized immutable execution snapshot over HTTP.
package strategy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/platform/token"
	"time"
)

type Client struct {
	origin string
	http   *http.Client
}

func New(origin string) (*Client, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("STRATEGY_SERVICE_URL must be an HTTP(S) origin")
	}
	u.Path = ""
	return &Client{u.String(), &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Snapshot(ctx context.Context, id, version string) (domain.Snapshot, error) {
	var out domain.Snapshot
	b, _ := json.Marshal(map[string]string{"resource_id": id, "version_id": version})
	req, e := http.NewRequestWithContext(ctx, "POST", c.origin+"/catalogue/execution-snapshot", bytes.NewReader(b))
	if e != nil {
		return out, domain.ErrUnavailable
	}
	req.Header.Set("Authorization", token.Authorization(ctx))
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return out, domain.ErrUnavailable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 404:
		return out, domain.ErrNotFound
	case 401, 403:
		return out, domain.ErrUnauthorized
	case 200:
	default:
		return out, domain.ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 700001))
	if e != nil || len(raw) > 700000 {
		return out, domain.ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e = decoder.Decode(&out); e != nil || decoder.Decode(new(any)) != io.EOF || out.Parameters == nil || out.Language != "python" || len(out.Source) == 0 || len(out.Source) > 100000 {
		return out, domain.ErrUnavailable
	}
	return out, nil
}
