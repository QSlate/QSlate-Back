package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"qslate/services/strategy/internal/domain"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type Strategies struct {
	repo     domain.Repository
	cfg      Config
	analyzer domain.Analyzer
}

func New(repo domain.Repository, cfg Config, analyzer domain.Analyzer) (*Strategies, error) {
	if repo == nil {
		return nil, errors.New("Strategy repository required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Strategies{repo, cfg, analyzer}, nil
}
func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, message) }
func id() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func scope(ctx context.Context, uid int64, sid string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if uid <= 0 {
		return domain.ErrUnauthorized
	}
	if sid != "" && !idPattern.MatchString(sid) {
		return domain.ErrNotFound
	}
	return nil
}
func object(raw json.RawMessage, max int) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > max || raw[0] != '{' || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, invalid("parameters must be a bounded JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, invalid("invalid parameters")
	}
	var valid func(any) bool
	valid = func(v any) bool {
		switch x := v.(type) {
		case string:
			return !strings.ContainsRune(x, 0)
		case json.Number:
			if i := strings.IndexAny(string(x), "eE"); i >= 0 {
				n, e := strconv.Atoi(string(x)[i+1:])
				return e == nil && n >= -10000 && n <= 10000
			}
		case []any:
			for _, child := range x {
				if !valid(child) {
					return false
				}
			}
		case map[string]any:
			for key, child := range x {
				if strings.ContainsRune(key, 0) || !valid(child) {
					return false
				}
			}
		}
		return true
	}
	if !valid(value) {
		return nil, invalid("parameters contain unsupported values")
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, invalid("invalid parameters")
	}
	result := bytes.TrimSpace(out.Bytes())
	if len(result) > max {
		return nil, invalid("parameters too large")
	}
	return json.RawMessage(result), nil
}
func source(value string, max int, empty bool) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || len(value) > max || (!empty && strings.TrimSpace(value) == "") {
		return invalid("source must be bounded UTF-8 Python text without NUL")
	}
	return nil
}
func normalize(in domain.Input, cfg Config) (domain.Input, error) {
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); !utf8.ValidString(in.Name) || n < 1 || n > 150 {
		return in, invalid("name must contain 1-150 printable characters")
	}
	for _, r := range in.Name {
		if !unicode.IsPrint(r) {
			return in, invalid("name must be printable")
		}
	}
	if err := source(in.Source, cfg.MaxSourceBytes, false); err != nil {
		return in, err
	}
	var err error
	in.Parameters, err = object(in.Parameters, cfg.MaxParametersBytes)
	return in, err
}
func (s *Strategies) Create(ctx context.Context, uid int64, in domain.Input) (domain.Strategy, error) {
	if err := scope(ctx, uid, ""); err != nil {
		return domain.Strategy{}, err
	}
	in, err := normalize(in, s.cfg)
	if err != nil {
		return domain.Strategy{}, err
	}
	sid, err := id()
	if err != nil {
		return domain.Strategy{}, err
	}
	vid, err := id()
	if err != nil {
		return domain.Strategy{}, err
	}
	return s.repo.Create(ctx, uid, sid, vid, in)
}
func (s *Strategies) List(ctx context.Context, uid int64, cursor string, limit int) (domain.Page, error) {
	if err := scope(ctx, uid, ""); err != nil {
		return domain.Page{}, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 || (cursor != "" && !idPattern.MatchString(cursor)) {
		return domain.Page{}, invalid("invalid pagination")
	}
	return s.repo.List(ctx, uid, cursor, limit)
}
func (s *Strategies) Get(ctx context.Context, uid int64, sid string) (domain.Strategy, error) {
	if err := scope(ctx, uid, sid); err != nil {
		return domain.Strategy{}, err
	}
	if sid == "" {
		return domain.Strategy{}, domain.ErrNotFound
	}
	return s.repo.Get(ctx, uid, sid)
}
func (s *Strategies) Save(ctx context.Context, uid int64, sid string, in domain.SaveInput) (domain.Strategy, error) {
	if err := scope(ctx, uid, sid); err != nil {
		return domain.Strategy{}, err
	}
	if !idPattern.MatchString(in.BaseVersionID) {
		return domain.Strategy{}, invalid("base_version_id required")
	}
	normalized, err := normalize(in.Input, s.cfg)
	if err != nil {
		return domain.Strategy{}, err
	}
	in.Input = normalized
	vid, err := id()
	if err != nil {
		return domain.Strategy{}, err
	}
	return s.repo.Save(ctx, uid, sid, vid, in)
}
func (s *Strategies) Delete(ctx context.Context, uid int64, sid string) error {
	if err := scope(ctx, uid, sid); err != nil {
		return err
	}
	return s.repo.Delete(ctx, uid, sid)
}
func (s *Strategies) Versions(ctx context.Context, uid int64, sid, cursor string, limit int) (domain.VersionPage, error) {
	if err := scope(ctx, uid, sid); err != nil {
		return domain.VersionPage{}, err
	}
	if limit == 0 {
		limit = s.cfg.DefaultVersionPageSize
	}
	before := int64(0)
	var err error
	if cursor != "" {
		before, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || before < 1 || strconv.FormatInt(before, 10) != cursor {
			return domain.VersionPage{}, invalid("invalid version cursor")
		}
	}
	if limit < 1 || limit > s.cfg.MaxVersionPageSize {
		return domain.VersionPage{}, invalid("invalid version page size")
	}
	return s.repo.Versions(ctx, uid, sid, before, limit)
}
func (s *Strategies) Restore(ctx context.Context, uid int64, sid string, in domain.RestoreInput) (domain.Strategy, error) {
	if err := scope(ctx, uid, sid); err != nil {
		return domain.Strategy{}, err
	}
	if !idPattern.MatchString(in.VersionID) || !idPattern.MatchString(in.BaseVersionID) {
		return domain.Strategy{}, invalid("version_id and base_version_id required")
	}
	vid, err := id()
	if err != nil {
		return domain.Strategy{}, err
	}
	return s.repo.Restore(ctx, uid, sid, vid, in)
}
func (s *Strategies) Lint(ctx context.Context, uid int64, in domain.LintInput) (domain.Diagnostics, error) {
	if err := scope(ctx, uid, ""); err != nil {
		return domain.Diagnostics{}, err
	}
	if in.Language != "" && in.Language != "python" {
		return domain.Diagnostics{}, invalid("only Python diagnostics supported")
	}
	if err := source(in.Source, s.cfg.MaxSourceBytes, true); err != nil {
		return domain.Diagnostics{}, err
	}
	in.Language = "python"
	if s.analyzer == nil {
		return domain.Diagnostics{}, domain.ErrUnavailable
	}
	return s.analyzer.Check(ctx, in)
}
