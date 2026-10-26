package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

type AnalysisConfig struct {
	Enabled           bool   `json:"enabled"`
	URLEnv            string `json:"url_env"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	AllowInsecureHTTP bool   `json:"allow_insecure_http"`
}
type Config struct {
	MaxSourceBytes         int            `json:"max_source_bytes"`
	MaxParametersBytes     int            `json:"max_parameters_bytes"`
	MaxBodyBytes           int64          `json:"max_body_bytes"`
	DefaultVersionPageSize int            `json:"default_version_page_size"`
	MaxVersionPageSize     int            `json:"max_version_page_size"`
	Analysis               AnalysisConfig `json:"analysis"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return c, err
	}
	if len(b) > 65536 || len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return c, errors.New("invalid Strategy configuration")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("expected one configuration object")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.MaxSourceBytes < 1 || c.MaxSourceBytes > 100000 || c.MaxParametersBytes < 2 || c.MaxParametersBytes > 4096 || c.MaxBodyBytes < int64(6*c.MaxSourceBytes+6*c.MaxParametersBytes+4096) || c.MaxBodyBytes > 2<<20 || c.DefaultVersionPageSize < 1 || c.DefaultVersionPageSize > c.MaxVersionPageSize || c.MaxVersionPageSize > 20 || c.Analysis.URLEnv == "" || c.Analysis.TimeoutSeconds < 1 || c.Analysis.TimeoutSeconds > 5 {
		return errors.New("invalid Strategy configuration limits")
	}
	return nil
}
