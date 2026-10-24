package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"qslate/services/widgets/internal/domain"
)

var referencePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
var customIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{43}$`)
var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9._:-]{16,128}$`)

type Widgets struct {
	repo    domain.Repository
	cfg     Config
	preview domain.Previewer
}

func New(repo domain.Repository, cfg Config, preview domain.Previewer) (*Widgets, error) {
	if repo == nil {
		return nil, errors.New("Widgets repository required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Widgets{repo, cfg, preview}, nil
}
func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, message) }
func id() (string, error) {
	for {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		value := base64.RawURLEncoding.EncodeToString(b[:])
		if referencePattern.MatchString(value) {
			return value, nil
		}
	}
}
func owner(ctx context.Context, uid int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if uid <= 0 {
		return domain.ErrUnauthorized
	}
	return nil
}
func scope(ctx context.Context, uid int64, key string) error {
	if err := owner(ctx, uid); err != nil {
		return err
	}
	if !customIDPattern.MatchString(key) {
		return domain.ErrNotFound
	}
	return nil
}

// Object preserves JSON integer precision and rejects values unsupported by jsonb.
func Object(raw json.RawMessage, optional bool, max int) (json.RawMessage, error) {
	if len(raw) == 0 && optional {
		return json.RawMessage(`{}`), nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > max || raw[0] != '{' || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, invalid("expected bounded JSON configuration object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object any
	if err := decoder.Decode(&object); err != nil {
		return nil, invalid("invalid JSON object")
	}
	var valid func(any) bool
	valid = func(v any) bool {
		switch x := v.(type) {
		case string:
			return !strings.ContainsRune(x, 0)
		case json.Number:
			if i := strings.IndexAny(string(x), "eE"); i >= 0 {
				n, err := strconv.Atoi(string(x)[i+1:])
				return err == nil && n >= -10000 && n <= 10000
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
	if !valid(object) {
		return nil, invalid("configuration contains NUL or unsupported numeric exponent")
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(object); err != nil {
		return nil, invalid("invalid JSON object")
	}
	result := bytes.TrimSpace(out.Bytes())
	if len(result) > max {
		return nil, invalid("normalized configuration too large")
	}
	return json.RawMessage(result), nil
}
func normalize(in domain.Input, c Config) (domain.Input, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !utf8.ValidString(in.Name) || utf8.RuneCountInString(in.Name) > 150 || strings.ContainsFunc(in.Name, unicode.IsControl) {
		return in, invalid("name must contain 1-150 printable characters")
	}
	if in.Language != "python" && in.Language != "rust" {
		return in, invalid("language must be python or rust")
	}
	if strings.TrimSpace(in.Source) == "" || !utf8.ValidString(in.Source) || len(in.Source) > c.MaxSourceBytes || strings.ContainsRune(in.Source, 0) {
		return in, invalid("source must be nonempty bounded UTF-8 text without NUL")
	}
	config, err := Object(in.Configuration, true, c.MaxConfigurationBytes)
	in.Configuration = config
	return in, err
}
func (s *Widgets) Sync(ctx context.Context) error {
	entries := make([]domain.Installed, len(s.cfg.Installed))
	copy(entries, s.cfg.Installed)
	for i := range entries {
		value, err := normalize(entries[i].Input, s.cfg)
		if err != nil {
			return err
		}
		entries[i].Input = value
	}
	return s.repo.SyncInstalled(ctx, entries)
}
func (s *Widgets) Create(ctx context.Context, uid int64, in domain.Input) (domain.Widget, error) {
	if err := owner(ctx, uid); err != nil {
		return domain.Widget{}, err
	}
	in, err := normalize(in, s.cfg)
	if err != nil {
		return domain.Widget{}, err
	}
	wid, err := id()
	if err != nil {
		return domain.Widget{}, err
	}
	version, err := id()
	if err != nil {
		return domain.Widget{}, err
	}
	return s.repo.Create(ctx, uid, wid, version, in)
}
func (s *Widgets) List(ctx context.Context, uid int64, installed bool, cursor string, limit int) (domain.Page, error) {
	if err := owner(ctx, uid); err != nil {
		return domain.Page{}, err
	}
	if cursor != "" && !referencePattern.MatchString(cursor) {
		return domain.Page{}, invalid("invalid cursor")
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return domain.Page{}, invalid("limit must be between 1 and 1000")
	}
	return s.repo.List(ctx, uid, installed, cursor, limit)
}
func (s *Widgets) Get(ctx context.Context, uid int64, wid string) (domain.Widget, error) {
	if err := scope(ctx, uid, wid); err != nil {
		return domain.Widget{}, err
	}
	return s.repo.Get(ctx, uid, wid)
}
func (s *Widgets) Replace(ctx context.Context, uid int64, wid string, in domain.Input) (domain.Widget, error) {
	if err := scope(ctx, uid, wid); err != nil {
		return domain.Widget{}, err
	}
	in, err := normalize(in, s.cfg)
	if err != nil {
		return domain.Widget{}, err
	}
	version, err := id()
	if err != nil {
		return domain.Widget{}, err
	}
	return s.repo.Replace(ctx, uid, wid, version, in)
}
func (s *Widgets) Delete(ctx context.Context, uid int64, wid string) error {
	if err := scope(ctx, uid, wid); err != nil {
		return err
	}
	return s.repo.Delete(ctx, uid, wid)
}
func (s *Widgets) Resolve(ctx context.Context, uid int64, wid, version string, existing bool) (domain.Reference, error) {
	if err := owner(ctx, uid); err != nil {
		return domain.Reference{}, err
	}
	if !referencePattern.MatchString(wid) || !referencePattern.MatchString(version) {
		return domain.Reference{}, domain.ErrNotFound
	}
	return s.repo.Resolve(ctx, uid, wid, version, existing)
}

func (s *Widgets) Preview(ctx context.Context, uid int64, wid, key string, in domain.PreviewInput) (domain.Job, error) {
	widget, err := s.Get(ctx, uid, wid)
	if err != nil {
		return domain.Job{}, err
	}
	if !referencePattern.MatchString(in.DatasetID) {
		return domain.Job{}, invalid("dataset_id is required")
	}
	in.Parameters, err = Object(in.Parameters, true, s.cfg.MaxConfigurationBytes)
	if err != nil {
		return domain.Job{}, err
	}
	if key == "" {
		key, err = id()
		if err != nil {
			return domain.Job{}, err
		}
	} else if !keyPattern.MatchString(key) {
		return domain.Job{}, invalid("Idempotency-Key must contain 16-128 identifier characters")
	}
	if s.preview == nil {
		return domain.Job{}, domain.ErrUnavailable
	}
	// Bind replay identity to the authenticated owner and immutable source snapshot.
	data, _ := json.Marshal([]any{uid, wid, widget.VersionID, key, in})
	sum := sha256.Sum256(data)
	jobID := base64.RawURLEncoding.EncodeToString(sum[:])
	request := domain.PreviewRequest{JobID: jobID, OwnerID: uid, WidgetID: wid, VersionID: widget.VersionID, Input: domain.Input{Name: widget.Name, Language: widget.Language, Source: widget.Source, Configuration: widget.Configuration}, PreviewInput: in}
	return s.preview.Submit(ctx, request)
}
