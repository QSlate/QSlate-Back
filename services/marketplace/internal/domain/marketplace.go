package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalid      = errors.New("invalid marketplace input")
	ErrNotFound     = errors.New("item or version not found")
	ErrConflict     = errors.New("listing changed or already exists")
	ErrUnauthorized = errors.New("active session required")
	ErrForbidden    = errors.New("installation required; authors cannot review their own items")
	ErrUnavailable  = errors.New("artifact catalogue unavailable")
)

// Snapshot is private, immutable publication content, never public metadata.
type Snapshot struct {
	Name          string          `json:"name"`
	Language      string          `json:"language"`
	Source        string          `json:"source"`
	Configuration json.RawMessage `json:"configuration"`
}
type PublishInput struct {
	Kind        string `json:"kind"`
	ResourceID  string `json:"resource_id"`
	VersionID   string `json:"version_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Visibility  string `json:"visibility,omitempty"`
}
type Patch struct {
	BaseRevision int64   `json:"base_revision"`
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Category     *string `json:"category,omitempty"`
	Visibility   *string `json:"visibility,omitempty"`
	VersionID    *string `json:"version_id,omitempty"`
}
type Item struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	ResourceID    string    `json:"-"`
	VersionID     string    `json:"version_id"`
	AuthorID      int64     `json:"author_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Category      string    `json:"category"`
	Visibility    string    `json:"visibility"`
	Revision      int64     `json:"revision"`
	InstallCount  int64     `json:"install_count"`
	ReviewCount   int64     `json:"review_count"`
	AverageRating float64   `json:"average_rating"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type Filter struct {
	Query, Kind, Category, Cursor string
	Limit                         int
	Featured                      bool
	FeaturedIDs                   []string
}
type ReviewInput struct {
	Rating  int    `json:"rating"`
	Comment string `json:"comment"`
}
type Review struct {
	ID       string `json:"id"`
	AuthorID int64  `json:"author_id"`
	ReviewInput
	CreatedAt time.Time `json:"created_at"`
}
type ReviewPage struct {
	Items      []Review `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}
type Installation struct {
	ItemID      string    `json:"item_id"`
	VersionID   string    `json:"version_id"`
	Kind        string    `json:"kind"`
	CatalogID   string    `json:"catalog_id"`
	InstalledAt time.Time `json:"installed_at"`
	Snapshot
}
type InstallationPage struct {
	Items      []Installation `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}
type Artifacts interface {
	Snapshot(context.Context, string, string, string) (Snapshot, error)
}
type Repository interface {
	List(context.Context, Filter) (Page, error)
	Get(context.Context, string) (Item, error)
	Owned(context.Context, int64, string) (Item, error)
	Publish(context.Context, int64, string, PublishInput, Snapshot) (Item, error)
	Patch(context.Context, int64, string, Patch, *Snapshot) (Item, error)
	Delete(context.Context, int64, string) error
	Install(context.Context, int64, string, string) (Installation, error)
	Uninstall(context.Context, int64, string) error
	Reviews(context.Context, string, string, int) (ReviewPage, error)
	Review(context.Context, int64, string, string, ReviewInput) (Review, error)
	Installations(context.Context, int64, string, string, int) (InstallationPage, error)
	Installed(context.Context, int64, string, string, bool) (Installation, error)
}
