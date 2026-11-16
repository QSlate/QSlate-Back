package service

import (
	"context"
	"qslate/services/market-data/internal/domain"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Decoder interface {
	Parse(context.Context, string, []byte, *domain.Mapping) (domain.Parsed, error)
}
type Repository interface {
	Create(context.Context, int64, string, string, []byte, domain.Parsed, int, int64) (domain.Dataset, error)
	Get(context.Context, int64, string) (domain.Dataset, error)
	List(context.Context, int64, string, int) (domain.Page, error)
	Map(context.Context, int64, string, string, domain.Mapping, int) (domain.Dataset, error)
	Enqueue(context.Context, int64, string, string) (domain.Job, error)
	Delete(context.Context, int64, string) error
	Snapshot(context.Context, int64, string, string) (domain.Snapshot, error)
	Symbols(context.Context, int64) ([]domain.Symbol, error)
	History(context.Context, int64, string, string, string, time.Time, time.Time, int, int, *time.Time) (domain.History, error)
}
type Data struct {
	Repo    Repository
	Decoder Decoder
	Config  Config
}

func New(repo Repository, decoder Decoder, c Config) (*Data, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	return &Data{repo, decoder, c}, nil
}
func ValidKey(s string) bool {
	if len(s) < 16 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func (d *Data) Upload(ctx context.Context, owner int64, name, format string, raw []byte) (domain.Dataset, error) {
	name = strings.TrimSpace(name)
	if owner <= 0 {
		return domain.Dataset{}, domain.ErrUnauthorized
	}
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 150 || len(raw) == 0 || int64(len(raw)) > d.Config.MaxUploadBytes || (format != "csv" && format != "parquet") {
		return domain.Dataset{}, domain.ErrInvalid
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return domain.Dataset{}, domain.ErrInvalid
		}
	}
	parsed, e := d.Decoder.Parse(ctx, format, raw, nil)
	if e != nil {
		return domain.Dataset{}, e
	}
	return d.Repo.Create(ctx, owner, name, format, raw, parsed, d.Config.MaxDatasets, d.Config.MaxOwnerBytes)
}
func (d *Data) Map(ctx context.Context, owner int64, id, base string, m domain.Mapping) (domain.Dataset, error) {
	if !domain.ValidID(base) {
		return domain.Dataset{}, domain.ErrInvalid
	}
	current, e := d.Repo.Get(ctx, owner, id)
	if e != nil {
		return current, e
	}
	if e = ValidateMapping(m, current.Columns); e != nil {
		return current, e
	}
	return d.Repo.Map(ctx, owner, id, base, m, d.Config.MaxVersions)
}
func (d *Data) Validate(ctx context.Context, owner int64, id, key string) (domain.Job, error) {
	if !ValidKey(key) {
		return domain.Job{}, domain.ErrInvalid
	}
	return d.Repo.Enqueue(ctx, owner, id, key)
}

var Resolutions = map[string]int{"raw": 0, "1m": 60, "5m": 300, "15m": 900, "1h": 3600, "1d": 86400}
