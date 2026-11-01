package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"qslate/services/marketplace/internal/domain"
	"qslate/services/marketplace/internal/platform/token"
	"strings"
	"time"
	"unicode/utf8"
)

type Client struct {
	widgets, strategy string
	http              *http.Client
}

func New(widgets, strategy string, seconds int) (*Client, error) {
	if seconds < 1 || seconds > 5 {
		return nil, errors.New("artifact timeout must be 1-5 seconds")
	}
	origins := []string{widgets, strategy}
	for n, v := range origins {
		u, e := url.Parse(v)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, errors.New("artifact URLs must be HTTP(S) origins")
		}
		u.Path = ""
		origins[n] = u.String()
	}
	return &Client{origins[0], origins[1], &http.Client{Timeout: time.Duration(seconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Snapshot(ctx context.Context, kind, key, version string) (domain.Snapshot, error) {
	var s domain.Snapshot
	if token.Subject(ctx) <= 0 || token.Authorization(ctx) == "" {
		return s, domain.ErrUnauthorized
	}
	origin := c.widgets
	if kind == "strategy" {
		origin = c.strategy
	}
	b, _ := json.Marshal(map[string]string{"resource_id": key, "version_id": version})
	req, e := http.NewRequestWithContext(ctx, "POST", origin+"/catalogue/snapshot", bytes.NewReader(b))
	if e != nil {
		return s, domain.ErrUnavailable
	}
	req.Header.Set("Authorization", token.Authorization(ctx))
	req.Header.Set("Content-Type", "application/json")
	r, e := c.http.Do(req)
	if e != nil {
		return s, domain.ErrUnavailable
	}
	defer r.Body.Close()
	switch r.StatusCode {
	case 401:
		return s, domain.ErrUnauthorized
	case 404:
		return s, domain.ErrNotFound
	case 200:
	default:
		return s, domain.ErrUnavailable
	}
	b, e = io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return s, domain.ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF {
		return s, domain.ErrUnavailable
	}
	cfg := bytes.TrimSpace(s.Configuration)
	var compact bytes.Buffer
	if json.Compact(&compact, cfg) != nil {
		return s, domain.ErrUnavailable
	}
	cfg = compact.Bytes()
	s.Configuration = cfg
	if !utf8.ValidString(s.Source) || strings.TrimSpace(s.Source) == "" || strings.ContainsRune(s.Source, 0) || len(s.Source) > 100000 || len(s.Name) > 600 || len(cfg) < 2 || len(cfg) > 4096 || cfg[0] != '{' || !json.Valid(cfg) || (s.Language != "python" && s.Language != "rust") || (kind == "strategy" && s.Language != "python") {
		return s, domain.ErrUnavailable
	}
	return s, nil
}
