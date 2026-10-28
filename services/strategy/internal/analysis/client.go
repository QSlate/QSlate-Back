package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"qslate/services/strategy/internal/domain"
	"qslate/services/strategy/internal/service"
	"strings"
	"time"
)

type Client struct {
	url  string
	http *http.Client
}

func New(cfg service.AnalysisConfig) (*Client, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	u, err := url.Parse(os.Getenv(cfg.URLEnv))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && cfg.AllowInsecureHTTP)) || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("valid analyzer origin required; HTTP requires explicit opt-in")
	}
	if cfg.TimeoutSeconds < 1 || cfg.TimeoutSeconds > 5 {
		return nil, errors.New("invalid analysis timeout")
	}
	return &Client{strings.TrimRight(u.String(), "/"), &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.url+"/readyz", nil)
	if err != nil {
		return domain.ErrUnavailable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return domain.ErrUnavailable
	}
	return nil
}
func (c *Client) Check(ctx context.Context, in domain.LintInput) (domain.Diagnostics, error) {
	result := domain.Diagnostics{}
	body, err := json.Marshal(in)
	if err != nil {
		return result, domain.ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/lint", bytes.NewReader(body))
	if err != nil {
		return result, domain.ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	// No user token, owner, database credential or arbitrary URL is forwarded.
	resp, err := c.http.Do(req)
	if err != nil {
		return result, domain.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return result, domain.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(data) > 16384 {
		return result, domain.ErrUnavailable
	}
	var wire struct {
		Valid       *bool                `json:"valid"`
		Diagnostics *[]domain.Diagnostic `json:"diagnostics"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&wire) != nil || d.Decode(new(any)) != io.EOF || wire.Valid == nil || wire.Diagnostics == nil || len(*wire.Diagnostics) > 50 {
		return result, domain.ErrUnavailable
	}
	result.Valid = *wire.Valid
	result.Diagnostics = *wire.Diagnostics
	hasError := false
	for _, v := range result.Diagnostics {
		if v.Line < 1 || v.Column < 1 || v.EndLine < v.Line || v.EndColumn < 1 || (v.EndLine == v.Line && v.EndColumn < v.Column) || len(v.Message) == 0 || len(v.Message) > 2000 || (v.Severity != "error" && v.Severity != "warning" && v.Severity != "info") {
			return domain.Diagnostics{}, domain.ErrUnavailable
		}
		hasError = hasError || v.Severity == "error"
	}
	if result.Valid == hasError {
		return domain.Diagnostics{}, domain.ErrUnavailable
	}
	return result, nil
}
