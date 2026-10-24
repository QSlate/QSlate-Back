package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("dashboard not found")
	ErrInvalidInput = errors.New("invalid workspace input")
	ErrUnauthorized = errors.New("authenticated owner required")
	ErrConflict     = errors.New("dashboard revision conflict")
)

type Metadata struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type Summary struct {
	ID      string `json:"id"`
	OwnerID int64  `json:"-"`
	Metadata
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Widget struct {
	InstanceID      string          `json:"instance_id"`
	WidgetID        string          `json:"widget_id"`
	WidgetVersionID string          `json:"widget_version_id"`
	Configuration   json.RawMessage `json:"configuration"`
}

type LayoutItem struct {
	InstanceID string `json:"instance_id"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

type Dashboard struct {
	Summary
	Widgets []Widget     `json:"widgets"`
	Layout  []LayoutItem `json:"layout"`
}

type Page struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// Repository is private to Workspace. Owner scoping is mandatory on every query.
// Metadata replacement increments the revision reserved for optimistic layout writes.
type Repository interface {
	Create(context.Context, Dashboard) (Dashboard, error)
	List(context.Context, int64, string, int) (Page, error)
	Get(context.Context, int64, string) (Dashboard, error)
	ReplaceMetadata(context.Context, int64, string, Metadata) (Dashboard, error)
	// Mutate locks one owner-scoped dashboard; callback failure rolls back everything.
	// A successful mutation advances revision exactly once.
	Mutate(context.Context, int64, string, func(*Dashboard) error) (Dashboard, error)
	Delete(context.Context, int64, string) error
}
