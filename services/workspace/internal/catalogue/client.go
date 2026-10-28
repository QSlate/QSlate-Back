package catalogue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"qslate/internal/platform/token"
	"qslate/services/workspace/internal/domain"
	"time"
)

type Client struct {
	origin string
	http   *http.Client
}

func New(origin string) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("WIDGETS_SERVICE_URL must be an HTTP(S) origin")
	}
	u.Path = ""
	return &Client{u.String(), &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Validate(ctx context.Context, uid int64, wid, version string, existing bool) error {
	if token.Subject(ctx) != uid || uid <= 0 || token.Authorization(ctx) == "" {
		return domain.ErrUnauthorized
	}
	payload, _ := json.Marshal(map[string]any{"widget_id": wid, "widget_version_id": version, "existing": existing})
	req, err := http.NewRequestWithContext(ctx, "POST", c.origin+"/catalogue/resolve", bytes.NewReader(payload))
	if err != nil {
		return domain.ErrCatalogueUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token.Authorization(ctx))
	response, err := c.http.Do(req)
	if err != nil {
		return domain.ErrCatalogueUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 401:
		return domain.ErrUnauthorized
	case 404:
		return domain.ErrNotFound
	case 200:
	default:
		return domain.ErrCatalogueUnavailable
	}
	var ref struct {
		WidgetID  string `json:"widget_id"`
		VersionID string `json:"widget_version_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4096))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&ref); err != nil || ref.WidgetID != wid || ref.VersionID != version || decoder.Decode(new(any)) != io.EOF {
		return domain.ErrCatalogueUnavailable
	}
	return nil
}
func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.origin+"/readyz", nil)
	if err != nil {
		return domain.ErrCatalogueUnavailable
	}
	r, err := c.http.Do(req)
	if err != nil {
		return domain.ErrCatalogueUnavailable
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return domain.ErrCatalogueUnavailable
	}
	return nil
}
