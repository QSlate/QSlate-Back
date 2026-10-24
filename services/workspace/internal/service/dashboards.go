package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"qslate/services/workspace/internal/domain"
)

type Dashboards struct {
	repo   domain.Repository
	layout LayoutConfig
}

func New(repo domain.Repository, cfg LayoutConfig) (*Dashboards, error) {
	if repo == nil {
		return nil, errors.New("workspace repository required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Dashboards{repo: repo, layout: cfg}, nil
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{43}$`)

func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalidInput, message) }

func normalize(input domain.Metadata) (domain.Metadata, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if !printable(input.Title, 150) || input.Title == "" {
		return input, invalid("title must contain 1-150 printable characters")
	}
	if !utf8.ValidString(input.Description) || utf8.RuneCountInString(input.Description) > 2000 || strings.ContainsFunc(input.Description, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return input, invalid("description must contain at most 2000 characters without control characters")
	}
	if len(input.Tags) > 20 {
		return input, invalid("at most 20 tags are allowed")
	}
	tags := make([]string, 0, len(input.Tags))
	seen := map[string]bool{}
	for _, raw := range input.Tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if !printable(tag, 40) || tag == "" {
			return input, invalid("tags must contain 1-40 printable characters")
		}
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	input.Tags = tags
	return input, nil
}

func printable(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}

func scope(ctx context.Context, owner int64, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner <= 0 {
		return domain.ErrUnauthorized
	}
	if !idPattern.MatchString(id) {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Dashboards) Create(ctx context.Context, owner int64, input domain.Metadata) (domain.Dashboard, error) {
	if err := ctx.Err(); err != nil {
		return domain.Dashboard{}, err
	}
	if owner <= 0 {
		return domain.Dashboard{}, domain.ErrUnauthorized
	}
	metadata, err := normalize(input)
	if err != nil {
		return domain.Dashboard{}, err
	}
	id, err := newID()
	if err != nil {
		return domain.Dashboard{}, err
	}
	return s.repo.Create(ctx, domain.Dashboard{Summary: domain.Summary{ID: id, OwnerID: owner, Metadata: metadata}, Widgets: []domain.Widget{}, Layout: []domain.LayoutItem{}})
}

func newID() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (s *Dashboards) List(ctx context.Context, owner int64, cursor string, limit int) (domain.Page, error) {
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	if owner <= 0 {
		return domain.Page{}, domain.ErrUnauthorized
	}
	if cursor != "" && !idPattern.MatchString(cursor) {
		return domain.Page{}, invalid("invalid pagination cursor")
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return domain.Page{}, invalid("limit must be between 1 and 1000")
	}
	return s.repo.List(ctx, owner, cursor, limit)
}

func (s *Dashboards) Get(ctx context.Context, owner int64, id string) (domain.Dashboard, error) {
	if err := scope(ctx, owner, id); err != nil {
		return domain.Dashboard{}, err
	}
	return s.repo.Get(ctx, owner, id)
}

func (s *Dashboards) ReplaceMetadata(ctx context.Context, owner int64, id string, input domain.Metadata) (domain.Dashboard, error) {
	if err := scope(ctx, owner, id); err != nil {
		return domain.Dashboard{}, err
	}
	metadata, err := normalize(input)
	if err != nil {
		return domain.Dashboard{}, err
	}
	return s.repo.ReplaceMetadata(ctx, owner, id, metadata)
}

func (s *Dashboards) Delete(ctx context.Context, owner int64, id string) error {
	if err := scope(ctx, owner, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, owner, id)
}
