package decoder

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"qslate/services/market-data/internal/domain"
	"qslate/services/market-data/internal/service"
	"time"
)

type Client struct {
	origin string
	http   *http.Client
	cfg    service.Config
}

func New(origin string, c service.Config) (*Client, error) {
	s, e := service.Origin(origin, true)
	if e != nil {
		return nil, e
	}
	return &Client{s, &http.Client{Timeout: time.Duration(c.ParserTimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, c}, nil
}
func (c *Client) Parse(ctx context.Context, format string, raw []byte, mapping *domain.Mapping) (domain.Parsed, error) {
	var out domain.Parsed
	b, e := json.Marshal(map[string]any{"format": format, "data": raw, "mapping": mapping, "max_upload_bytes": c.cfg.MaxUploadBytes, "max_rows": c.cfg.MaxRows})
	if e != nil {
		return out, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.origin+"/parse", bytes.NewReader(b))
	if e != nil {
		return out, domain.ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return out, domain.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 400 {
		return out, domain.ErrInvalid
	}
	if resp.StatusCode != 200 {
		return out, domain.ErrUnavailable
	}
	b, e = io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if e != nil || len(b) > 32<<20 {
		return out, domain.ErrUnavailable
	}
	if e = json.Unmarshal(b, &out); e != nil || len(out.Columns) == 0 || len(out.Columns) > 64 || out.RowCount < 1 || out.RowCount > c.cfg.MaxRows || len(out.Preview) > 5 || (mapping != nil && len(out.Rows) != out.RowCount) {
		return out, domain.ErrUnavailable
	}
	return out, nil
}
