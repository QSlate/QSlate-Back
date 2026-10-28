// Package marketplace is a local HTTP adapter, not a shared runtime module.
package marketplace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"qslate/services/widgets/internal/platform/token"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrUnavailable  = errors.New("marketplace unavailable")
	ErrNotFound     = errors.New("installation not found")
	ErrUnauthorized = errors.New("active session required")
)
var refPattern = regexp.MustCompile(`^market-[A-Za-z0-9_-]{43}$`)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func IsReference(s string) bool { return refPattern.MatchString(s) }

type Client struct {
	origin string
	http   *http.Client
}

// An unset origin keeps a standalone service independent of Marketplace.
func New(origin string) (*Client, error) {
	if origin == "" {
		return nil, nil
	}
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("MARKETPLACE_SERVICE_URL must be an HTTP(S) origin")
	}
	u.Path = ""
	return &Client{u.String(), &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type Summary struct {
	ItemID      string    `json:"item_id"`
	VersionID   string    `json:"version_id"`
	Kind        string    `json:"kind"`
	CatalogID   string    `json:"catalog_id"`
	Name        string    `json:"name"`
	Language    string    `json:"language"`
	InstalledAt time.Time `json:"installed_at"`
}
type Page struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type Installation struct {
	Summary
	Source        string          `json:"source"`
	Configuration json.RawMessage `json:"configuration"`
}

func (c *Client) request(ctx context.Context, method, path string, in, out any) error {
	if token.Subject(ctx) <= 0 || token.Authorization(ctx) == "" {
		return ErrUnauthorized
	}
	b, e := json.Marshal(in)
	if e != nil {
		return ErrUnavailable
	}
	req, e := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(b))
	if e != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", token.Authorization(ctx))
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, e := c.http.Do(req)
	if e != nil {
		return ErrUnavailable
	}
	defer r.Body.Close()
	switch r.StatusCode {
	case 401:
		return ErrUnauthorized
	case 404:
		return ErrNotFound
	case 200:
	default:
		return ErrUnavailable
	}
	b, e = io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) List(ctx context.Context, kind, cursor string, limit int) (Page, error) {
	page := Page{Items: []Summary{}}
	if c == nil {
		return page, nil
	}
	marketCursor := ""
	if IsReference(cursor) {
		marketCursor = strings.TrimPrefix(cursor, "market-")
	} else if cursor != "" && cursor < "market-" {
		return page, nil
	}
	q := url.Values{"kind": {kind}, "limit": {strconv.Itoa(limit)}, "cursor": {marketCursor}}
	e := c.request(ctx, "GET", "/catalogue/installations?"+q.Encode(), nil, &page)
	if e != nil {
		return page, e
	}
	if len(page.Items) > limit {
		return page, ErrUnavailable
	}
	previous := cursor
	for _, v := range page.Items {
		if !IsReference(v.CatalogID) || v.CatalogID != "market-"+v.ItemID || !idPattern.MatchString(v.VersionID) || (kind == "strategy" && v.Kind != "strategy") || (kind == "widgets" && v.Kind != "widget" && v.Kind != "indicator") || len(v.Name) > 600 || !utf8.ValidString(v.Name) || (previous != "" && v.CatalogID >= previous) {
			return page, ErrUnavailable
		}
		previous = v.CatalogID
	}
	if page.NextCursor != "" {
		if len(page.Items) == 0 || page.NextCursor != page.Items[len(page.Items)-1].ItemID {
			return page, ErrUnavailable
		}
		page.NextCursor = "market-" + page.NextCursor
	}
	return page, nil
}
func (c *Client) Resolve(ctx context.Context, key, version string, existing bool) (Installation, error) {
	var v Installation
	if c == nil {
		return v, ErrNotFound
	}
	if !IsReference(key) || (version != "" && !idPattern.MatchString(version)) {
		return v, ErrNotFound
	}
	e := c.request(ctx, "POST", "/catalogue/resolve", map[string]any{"catalog_id": key, "version_id": version, "existing": existing}, &v)
	if e != nil {
		return v, e
	}
	cfg := bytes.TrimSpace(v.Configuration)
	var compact bytes.Buffer
	if json.Compact(&compact, cfg) != nil {
		return v, ErrUnavailable
	}
	cfg = compact.Bytes()
	v.Configuration = cfg
	if v.CatalogID != key || v.ItemID != strings.TrimPrefix(key, "market-") || !idPattern.MatchString(v.VersionID) || (version != "" && v.VersionID != version) || len(v.Source) > 100000 || !utf8.ValidString(v.Source) || strings.ContainsRune(v.Source, 0) || len(cfg) < 2 || len(cfg) > 4096 || cfg[0] != '{' || !json.Valid(cfg) || (v.Language != "python" && v.Language != "rust") {
		return v, ErrUnavailable
	}
	return v, nil
}
