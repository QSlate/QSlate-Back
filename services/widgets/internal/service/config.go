package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"qslate/services/widgets/internal/domain"
)

type PreviewConfig struct {
	Enabled           bool   `json:"enabled"`
	EndpointEnv       string `json:"endpoint_env"`
	TokenEnv          string `json:"token_env"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	AllowInsecureHTTP bool   `json:"allow_insecure_http"`
}
type Config struct {
	MaxSourceBytes        int                `json:"max_source_bytes"`
	MaxConfigurationBytes int                `json:"max_configuration_bytes"`
	MaxBodyBytes          int64              `json:"max_body_bytes"`
	Installed             []domain.Installed `json:"installed"`
	Preview               PreviewConfig      `json:"preview"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if len(data) > 2<<20 || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return c, errors.New("invalid Widgets config object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil {
		return c, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return c, errors.New("expected one Widgets config object")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.MaxSourceBytes < 1 || c.MaxSourceBytes > 100000 || c.MaxConfigurationBytes < 2 || c.MaxConfigurationBytes > 4096 || c.MaxBodyBytes < int64(6*c.MaxSourceBytes+6*c.MaxConfigurationBytes+4096) || c.MaxBodyBytes > 2<<20 || len(c.Installed) > 1000 || c.Preview.TimeoutSeconds < 1 || c.Preview.TimeoutSeconds > 5 || c.Preview.EndpointEnv == "" || c.Preview.TokenEnv == "" {
		return errors.New("invalid Widgets configuration limits")
	}
	ids, versions := map[string]bool{}, map[string]bool{}
	for _, entry := range c.Installed {
		if !referencePattern.MatchString(entry.ID) || !referencePattern.MatchString(entry.VersionID) || ids[entry.ID] || versions[entry.VersionID] {
			return errors.New("invalid or duplicate installed widget/version ID")
		}
		ids[entry.ID] = true
		versions[entry.VersionID] = true
		if _, err := normalize(entry.Input, c); err != nil {
			return err
		}
	}
	return nil
}
