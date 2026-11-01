// Package session checks active Auth sessions through its API, never its database.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

var ErrUnauthorized = errors.New("session is not active")
var ErrUnavailable = errors.New("session authority unavailable")

type Client struct {
	origin string
	http   *http.Client
}

func New(origin string) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("AUTH_SERVICE_URL must be an HTTP(S) origin without credentials, path or query")
	}
	u.Path = ""
	return &Client{origin: u.String(), http: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Validate(ctx context.Context, authorization string, owner int64) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.origin+"/users/me", nil)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", authorization)
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return ErrUnauthorized
	}
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	var profile struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	if err = decoder.Decode(&profile); err != nil || profile.User.ID <= 0 {
		return ErrUnavailable
	}
	if profile.User.ID != owner || owner <= 0 {
		return ErrUnauthorized
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return ErrUnavailable
	}
	return nil
}

func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.origin+"/readyz", nil)
	if err != nil {
		return ErrUnavailable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	return nil
}
