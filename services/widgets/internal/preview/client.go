// Package preview dispatches immutable snapshots to a trusted sandbox controller.
// It never compiles or executes submitted source. The controller owns durable
// jobs, dataset authorization, gVisor isolation, quotas and result access.
package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"qslate/services/widgets/internal/domain"
	"qslate/services/widgets/internal/service"
	"strings"
	"time"
)

type Client struct {
	url, token string
	http       *http.Client
}

func New(cfg service.PreviewConfig) (domain.Previewer, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	endpoint, secret := os.Getenv(cfg.EndpointEnv), os.Getenv(cfg.TokenEnv)
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Scheme != "https" && !(cfg.AllowInsecureHTTP && u.Scheme == "http")) || len(secret) < 32 || strings.ContainsAny(secret, "\r\n") {
		return nil, errors.New("enabled preview requires a valid controller URL and service credential of at least 32 characters")
	}
	return &Client{endpoint, secret, &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Submit(ctx context.Context, in domain.PreviewRequest) (domain.Job, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return domain.Job{}, domain.ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(payload))
	if err != nil {
		return domain.Job{}, domain.ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Idempotency-Key", in.JobID)
	response, err := c.http.Do(req)
	if err != nil {
		return domain.Job{}, domain.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return domain.Job{}, domain.ErrUnavailable
	}
	var job domain.Job
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4096))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&job); err != nil || job.JobID != in.JobID || job.Status != "queued" {
		return domain.Job{}, domain.ErrUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF {
		return domain.Job{}, domain.ErrUnavailable
	}
	return job, nil
}
