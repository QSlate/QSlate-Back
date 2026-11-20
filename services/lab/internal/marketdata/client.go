// Package marketdata freezes authorized ready datasets through their owning API.
package marketdata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	origin   string
	maxBytes int64
	http     *http.Client
}

func New(origin string, maxBytes int64) (*Client, error) {
	if origin == "" {
		return nil, nil
	}
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("MARKET_DATA_SERVICE_URL must be an HTTP(S) origin")
	}
	u.Path = ""
	return &Client{u.String(), maxBytes, &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Snapshot(ctx context.Context, id, version string) (string, string, error) {
	b, _ := json.Marshal(map[string]string{"dataset_id": id, "version_id": version})
	req, e := http.NewRequestWithContext(ctx, "POST", c.origin+"/catalogue/dataset-snapshot", bytes.NewReader(b))
	if e != nil {
		return "", "", domain.ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token.Authorization(ctx))
	resp, e := c.http.Do(req)
	if e != nil {
		return "", "", domain.ErrUnavailable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 404:
		return "", "", domain.ErrNotFound
	case 409:
		return "", "", domain.ErrConflict
	case 401, 403:
		return "", "", domain.ErrUnauthorized
	case 200:
	default:
		return "", "", domain.ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes*2+4097))
	if e != nil || int64(len(raw)) > c.maxBytes*2+4096 {
		return "", "", domain.ErrUnavailable
	}
	var out struct {
		ID        string `json:"dataset_id"`
		VersionID string `json:"version_id"`
		CSV       string `json:"csv"`
		SHA256    string `json:"sha256"`
	}
	if json.Unmarshal(raw, &out) != nil || out.ID != id || out.VersionID != version || int64(len(out.CSV)) > c.maxBytes || out.CSV == "" {
		return "", "", domain.ErrUnavailable
	}
	sum := sha256.Sum256([]byte(out.CSV))
	if hex.EncodeToString(sum[:]) != out.SHA256 {
		return "", "", domain.ErrUnavailable
	}
	return out.CSV, out.SHA256, nil
}
