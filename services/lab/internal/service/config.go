package service

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const EngineRevision = "fb63ed3180ac263019c1b6524da90826f6b316a2"

type Dataset struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
	File      string `json:"file"`
	SHA256    string `json:"sha256"`
	OwnerID   int64  `json:"owner_id,omitempty"`
}
type ExecutionConfig struct {
	Enabled        bool   `json:"enabled"`
	Image          string `json:"image"`
	Runtime        string `json:"runtime"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MemoryMB       int    `json:"memory_mb"`
	CPUs           int    `json:"cpus"`
	PIDs           int    `json:"pids"`
	WorkMB         int    `json:"work_mb"`
}
type Config struct {
	MaxDatasetBytes     int64           `json:"max_dataset_bytes"`
	MaxDatasetRows      int             `json:"max_dataset_rows"`
	MaxArtifactBytes    int             `json:"max_artifact_bytes"`
	MaxLogBytes         int             `json:"max_log_bytes"`
	MaxOptimizationRuns int             `json:"max_optimization_runs"`
	MaxOutstandingRuns  int             `json:"max_outstanding_runs"`
	MaxPageSize         int             `json:"max_page_size"`
	DataRoot            string          `json:"data_root"`
	Datasets            []Dataset       `json:"datasets"`
	AllowedOrigins      []string        `json:"allowed_origins"`
	Execution           ExecutionConfig `json:"execution"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if e != nil {
		return c, e
	}
	if len(b) > 1<<20 {
		return c, errors.New("Lab configuration too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("expected one configuration object")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	e := c.Execution
	if c.MaxDatasetBytes < 1 || c.MaxDatasetBytes > 16<<20 || c.MaxDatasetRows < 2 || c.MaxDatasetRows > 100000 || c.MaxArtifactBytes < 1024 || c.MaxArtifactBytes > 32<<20 || c.MaxLogBytes < 1 || c.MaxLogBytes > 65536 || c.MaxOptimizationRuns < 1 || c.MaxOptimizationRuns > 100 || c.MaxOutstandingRuns < c.MaxOptimizationRuns || c.MaxOutstandingRuns > 1000 || c.MaxPageSize < 1 || c.MaxPageSize > 1000 || c.DataRoot == "" || e.TimeoutSeconds < 1 || e.TimeoutSeconds > 600 || e.MemoryMB < 128 || e.MemoryMB > 4096 || e.CPUs < 1 || e.CPUs > 8 || e.PIDs < 16 || e.PIDs > 256 || e.WorkMB < 16 || e.WorkMB > 256 || e.Runtime != "runsc" || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/_.:@-]{0,255}$`).MatchString(e.Image) {
		return errors.New("invalid Lab limits; isolated runsc runtime required")
	}
	seen := map[string]bool{}
	for _, d := range c.Datasets {
		sum, err := hex.DecodeString(d.SHA256)
		key := d.ID + "/" + d.VersionID
		if d.ID == "" || len(d.ID) > 128 || d.VersionID == "" || len(d.VersionID) > 128 || d.OwnerID < 0 || !filepath.IsLocal(d.File) || strings.Contains(d.File, "\\") || err != nil || len(sum) != 32 || seen[key] {
			return errors.New("invalid or duplicate versioned dataset")
		}
		seen[key] = true
	}
	return nil
}
