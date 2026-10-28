package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"qslate/services/marketplace/internal/domain"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var IDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var categoryPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Config struct {
	MaxBodyBytes           int64      `json:"max_body_bytes"`
	MaxDescriptionChars    int        `json:"max_description_chars"`
	MaxCommentChars        int        `json:"max_comment_chars"`
	DefaultPageSize        int        `json:"default_page_size"`
	MaxPageSize            int        `json:"max_page_size"`
	Categories             []Category `json:"categories"`
	FeaturedIDs            []string   `json:"featured_ids"`
	ArtifactTimeoutSeconds int        `json:"artifact_timeout_seconds"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if len(b) > 65536 || len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return c, errors.New("invalid Marketplace config")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("one config object required")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.MaxBodyBytes < 65536 || c.MaxBodyBytes > 2<<20 || c.MaxDescriptionChars < 1 || c.MaxDescriptionChars > 10000 || c.MaxCommentChars < 1 || c.MaxCommentChars > 4000 || c.DefaultPageSize < 1 || c.DefaultPageSize > c.MaxPageSize || c.MaxPageSize > 1000 || c.ArtifactTimeoutSeconds < 1 || c.ArtifactTimeoutSeconds > 5 || len(c.Categories) < 1 || len(c.Categories) > 100 || len(c.FeaturedIDs) > 100 {
		return errors.New("invalid Marketplace limits")
	}
	seen := map[string]bool{}
	for _, v := range c.Categories {
		if !categoryPattern.MatchString(v.ID) || strings.TrimSpace(v.Name) == "" || !text(v.Name, 80, false) || seen[v.ID] {
			return errors.New("invalid categories")
		}
		seen[v.ID] = true
	}
	for _, id := range c.FeaturedIDs {
		if !IDPattern.MatchString(id) {
			return errors.New("invalid featured item ID")
		}
	}
	return nil
}

type Marketplace struct {
	repo      domain.Repository
	artifacts domain.Artifacts
	cfg       Config
}

func New(repo domain.Repository, artifacts domain.Artifacts, cfg Config) (*Marketplace, error) {
	if repo == nil || artifacts == nil {
		return nil, errors.New("repository and artifact boundary required")
	}
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	return &Marketplace{repo, artifacts, cfg}, nil
}
func invalid(m string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, m) }
func text(v string, max int, multiline bool) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) <= max && !strings.ContainsFunc(v, func(r rune) bool {
		return unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t' || r == '\r'))
	})
}
func owner(uid int64) error {
	if uid <= 0 {
		return domain.ErrUnauthorized
	}
	return nil
}
func id() (string, error) {
	var b [32]byte
	_, e := rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:]), e
}
func (s *Marketplace) category(v string) bool {
	for _, c := range s.cfg.Categories {
		if c.ID == v {
			return true
		}
	}
	return false
}
func kind(v string) bool { return v == "widget" || v == "indicator" || v == "strategy" }
func (s *Marketplace) Page(cursor string, limit int) (int, error) {
	if limit == 0 {
		limit = s.cfg.DefaultPageSize
	}
	if limit < 1 || limit > s.cfg.MaxPageSize || (cursor != "" && !IDPattern.MatchString(cursor)) {
		return 0, invalid("invalid pagination")
	}
	return limit, nil
}
func (s *Marketplace) List(ctx context.Context, f domain.Filter) (domain.Page, error) {
	n, e := s.Page(f.Cursor, f.Limit)
	if e != nil {
		return domain.Page{}, e
	}
	f.Limit = n
	f.Query = strings.TrimSpace(f.Query)
	if !text(f.Query, 200, false) || (f.Kind != "" && !kind(f.Kind)) || (f.Category != "" && !s.category(f.Category)) {
		return domain.Page{}, invalid("invalid search/filter")
	}
	f.FeaturedIDs = s.cfg.FeaturedIDs
	return s.repo.List(ctx, f)
}
func (s *Marketplace) Get(ctx context.Context, key string) (domain.Item, error) {
	if !IDPattern.MatchString(key) {
		return domain.Item{}, domain.ErrNotFound
	}
	return s.repo.Get(ctx, key)
}
func (s *Marketplace) Publish(ctx context.Context, uid int64, in domain.PublishInput) (domain.Item, error) {
	if e := owner(uid); e != nil {
		return domain.Item{}, e
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Visibility == "" {
		in.Visibility = "public"
	}
	if !kind(in.Kind) || !IDPattern.MatchString(in.ResourceID) || !IDPattern.MatchString(in.VersionID) || in.Name == "" || !text(in.Name, 150, false) || !text(in.Description, s.cfg.MaxDescriptionChars, true) || !s.category(in.Category) || (in.Visibility != "public" && in.Visibility != "unlisted") {
		return domain.Item{}, invalid("invalid listing fields")
	}
	snapshot, e := s.artifacts.Snapshot(ctx, in.Kind, in.ResourceID, in.VersionID)
	if e != nil {
		return domain.Item{}, e
	}
	key, e := id()
	if e != nil {
		return domain.Item{}, e
	}
	return s.repo.Publish(ctx, uid, key, in, snapshot)
}
func (s *Marketplace) Patch(ctx context.Context, uid int64, key string, in domain.Patch) (domain.Item, error) {
	if e := owner(uid); e != nil {
		return domain.Item{}, e
	}
	if !IDPattern.MatchString(key) {
		return domain.Item{}, domain.ErrNotFound
	}
	if in.BaseRevision < 1 || (in.Name == nil && in.Description == nil && in.Category == nil && in.Visibility == nil && in.VersionID == nil) {
		return domain.Item{}, invalid("base_revision and at least one field required")
	}
	if in.Name != nil {
		v := strings.TrimSpace(*in.Name)
		in.Name = &v
		if v == "" || !text(v, 150, false) {
			return domain.Item{}, invalid("invalid name")
		}
	}
	if in.Description != nil && !text(*in.Description, s.cfg.MaxDescriptionChars, true) || in.Category != nil && !s.category(*in.Category) || in.Visibility != nil && *in.Visibility != "public" && *in.Visibility != "unlisted" || in.VersionID != nil && !IDPattern.MatchString(*in.VersionID) {
		return domain.Item{}, invalid("invalid listing fields")
	}
	var snapshot *domain.Snapshot
	if in.VersionID != nil {
		old, e := s.repo.Owned(ctx, uid, key)
		if e != nil {
			return domain.Item{}, e
		}
		if old.Revision != in.BaseRevision {
			return domain.Item{}, domain.ErrConflict
		}
		v, e := s.artifacts.Snapshot(ctx, old.Kind, old.ResourceID, *in.VersionID)
		if e != nil {
			return domain.Item{}, e
		}
		snapshot = &v
	}
	return s.repo.Patch(ctx, uid, key, in, snapshot)
}
func (s *Marketplace) Delete(ctx context.Context, uid int64, key string) error {
	if e := owner(uid); e != nil {
		return e
	}
	if !IDPattern.MatchString(key) {
		return domain.ErrNotFound
	}
	return s.repo.Delete(ctx, uid, key)
}
func (s *Marketplace) Install(ctx context.Context, uid int64, key, version string) (domain.Installation, error) {
	if e := owner(uid); e != nil {
		return domain.Installation{}, e
	}
	if !IDPattern.MatchString(key) {
		return domain.Installation{}, domain.ErrNotFound
	}
	if !IDPattern.MatchString(version) {
		return domain.Installation{}, invalid("version_id required")
	}
	return s.repo.Install(ctx, uid, key, version)
}
func (s *Marketplace) Uninstall(ctx context.Context, uid int64, key string) error {
	if e := owner(uid); e != nil {
		return e
	}
	if !IDPattern.MatchString(key) {
		return domain.ErrNotFound
	}
	return s.repo.Uninstall(ctx, uid, key)
}
func (s *Marketplace) Reviews(ctx context.Context, key, cursor string, limit int) (domain.ReviewPage, error) {
	if !IDPattern.MatchString(key) {
		return domain.ReviewPage{}, domain.ErrNotFound
	}
	n, e := s.Page(cursor, limit)
	if e != nil {
		return domain.ReviewPage{}, e
	}
	return s.repo.Reviews(ctx, key, cursor, n)
}
func (s *Marketplace) Review(ctx context.Context, uid int64, key string, in domain.ReviewInput) (domain.Review, error) {
	if e := owner(uid); e != nil {
		return domain.Review{}, e
	}
	if !IDPattern.MatchString(key) {
		return domain.Review{}, domain.ErrNotFound
	}
	if in.Rating < 1 || in.Rating > 5 || !text(in.Comment, s.cfg.MaxCommentChars, true) {
		return domain.Review{}, invalid("rating 1-5 and bounded comment required")
	}
	rid, e := id()
	if e != nil {
		return domain.Review{}, e
	}
	return s.repo.Review(ctx, uid, key, rid, in)
}
func (s *Marketplace) Installations(ctx context.Context, uid int64, k, cursor string, limit int) (domain.InstallationPage, error) {
	if e := owner(uid); e != nil {
		return domain.InstallationPage{}, e
	}
	n, e := s.Page(cursor, limit)
	if e != nil {
		return domain.InstallationPage{}, e
	}
	if k != "" && k != "widgets" && !kind(k) {
		return domain.InstallationPage{}, domain.ErrInvalid
	}
	return s.repo.Installations(ctx, uid, k, cursor, n)
}
func (s *Marketplace) Installed(ctx context.Context, uid int64, key, version string, existing bool) (domain.Installation, error) {
	if e := owner(uid); e != nil {
		return domain.Installation{}, e
	}
	key = strings.TrimPrefix(key, "market-")
	if !IDPattern.MatchString(key) || (version != "" && !IDPattern.MatchString(version)) || (existing && version == "") {
		return domain.Installation{}, domain.ErrNotFound
	}
	return s.repo.Installed(ctx, uid, key, version, existing)
}
