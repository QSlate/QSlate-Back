package service

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"qslate/services/market-data/internal/domain"
	"regexp"
	"time"
	_ "time/tzdata"
)

type Feed struct {
	Symbol  string `json:"symbol"`
	Name    string `json:"name"`
	OwnerID int64  `json:"owner_id"`
}
type LiveConfig struct {
	Enabled     bool   `json:"enabled"`
	OriginEnv   string `json:"origin_env"`
	TokenEnv    string `json:"token_env"`
	AllowHTTP   bool   `json:"allow_insecure_http"`
	PollSeconds int    `json:"poll_seconds"`
	MaxSymbols  int    `json:"max_symbols"`
	MaxStreams  int    `json:"max_streams"`
	Symbols     []Feed `json:"symbols"`
}
type PublicDataset struct {
	ID         string `json:"id"`
	VersionID  string `json:"version_id"`
	Symbol     string `json:"symbol"`
	Name       string `json:"name"`
	Resolution string `json:"resolution"`
	File       string `json:"file"`
	SHA256     string `json:"sha256"`
}
type Config struct {
	MaxUploadBytes       int64           `json:"max_upload_bytes"`
	MaxRows              int             `json:"max_rows"`
	MaxDatasets          int             `json:"max_datasets"`
	MaxOwnerBytes        int64           `json:"max_owner_bytes"`
	MaxVersions          int             `json:"max_versions"`
	MaxPageSize          int             `json:"max_page_size"`
	ParserTimeoutSeconds int             `json:"parser_timeout_seconds"`
	MaxSnapshotBytes     int             `json:"max_snapshot_bytes"`
	RequireTimescale     bool            `json:"require_timescale"`
	AllowedOrigins       []string        `json:"allowed_origins"`
	PublicDataRoot       string          `json:"public_data_root"`
	PublicDatasets       []PublicDataset `json:"public_datasets"`
	Live                 LiveConfig      `json:"live"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 {
		return c, errors.New("invalid market data config")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("expected one configuration")
	}
	if c.PublicDataRoot != "" && !filepath.IsAbs(c.PublicDataRoot) {
		c.PublicDataRoot = filepath.Clean(filepath.Join(filepath.Dir(path), c.PublicDataRoot))
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.MaxUploadBytes < 1024 || c.MaxUploadBytes > 100<<20 || c.MaxRows < 1 || c.MaxRows > 100000 || c.MaxDatasets < 1 || c.MaxDatasets > 100 || c.MaxOwnerBytes < c.MaxUploadBytes || c.MaxOwnerBytes > 1<<30 || c.MaxVersions < 1 || c.MaxVersions > 20 || c.MaxPageSize < 1 || c.MaxPageSize > 1000 || c.ParserTimeoutSeconds < 1 || c.ParserTimeoutSeconds > 8 || c.MaxSnapshotBytes < 1024 || c.MaxSnapshotBytes > 16<<20 || c.Live.PollSeconds < 1 || c.Live.PollSeconds > 30 || c.Live.MaxSymbols < 1 || c.Live.MaxSymbols > 50 || c.Live.MaxStreams < 1 || c.Live.MaxStreams > 128 {
		return errors.New("invalid market data limits")
	}
	seen := map[string]bool{}
	if c.PublicDataRoot == "" || len(c.PublicDatasets) == 0 || len(c.PublicDatasets) > 32 {
		return errors.New("public dataset catalogue required")
	}
	publicIDs, publicSymbols := map[string]bool{}, map[string]bool{}
	for _, d := range c.PublicDatasets {
		_, checksumErr := hex.DecodeString(d.SHA256)
		if !publicIDPattern.MatchString(d.ID) || !publicVersionPattern.MatchString(d.VersionID) || d.Symbol == "" || len(d.Symbol) > 64 || d.Name == "" || len(d.Name) > 150 || (d.Resolution != "1h" && d.Resolution != "1d") || !filepath.IsLocal(d.File) || filepath.Ext(d.File) != ".csv" || checksumErr != nil || len(d.SHA256) != 64 || publicIDs[d.ID] || publicSymbols[d.Symbol] {
			return errors.New("invalid public dataset catalogue")
		}
		publicIDs[d.ID], publicSymbols[d.Symbol] = true, true
	}
	for _, s := range c.Live.Symbols {
		if s.Symbol == "" || len(s.Symbol) > 64 || s.Name == "" || s.OwnerID < 0 || seen[s.Symbol] {
			return errors.New("invalid feed symbols")
		}
		seen[s.Symbol] = true
	}
	return nil
}

var publicIDPattern = regexp.MustCompile(`^public-[a-z0-9-]{1,72}$`)
var publicVersionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func ValidPublicID(s string) bool { return publicIDPattern.MatchString(s) }
func Origin(origin string, allowHTTP bool) (string, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(allowHTTP && u.Scheme == "http")) {
		return "", errors.New("explicit HTTP(S) origin required")
	}
	u.Path = ""
	return u.String(), nil
}
func ValidateMapping(m domain.Mapping, columns []string) error {
	if m.Timezone == "" || !validZone(m.Timezone) {
		return domain.ErrInvalid
	}
	switch m.TimestampUnit {
	case "iso8601", "s", "ms", "us", "ns":
	default:
		return domain.ErrInvalid
	}
	available := map[string]bool{}
	for _, c := range columns {
		available[c] = true
	}
	used := map[string]bool{}
	for _, c := range []string{m.TimestampColumn, m.Columns.Open, m.Columns.High, m.Columns.Low, m.Columns.Close, m.Columns.Volume} {
		if c == "" {
			continue
		}
		if !available[c] || used[c] {
			return domain.ErrInvalid
		}
		used[c] = true
	}
	if m.TimestampColumn == "" || m.Columns.Open == "" || m.Columns.High == "" || m.Columns.Low == "" || m.Columns.Close == "" {
		return domain.ErrInvalid
	}
	return nil
}

// Keep temporal policy explicit: ISO values without an offset use the configured IANA zone.
func validZone(s string) bool { _, e := time.LoadLocation(s); return e == nil }
