package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("widget or version not found")
	ErrInvalid      = errors.New("invalid widget input")
	ErrUnauthorized = errors.New("authenticated owner required")
	ErrUnavailable  = errors.New("sandbox controller unavailable")
)

type Input struct {
	Name          string          `json:"name"`
	Language      string          `json:"language"`
	Source        string          `json:"source"`
	Configuration json.RawMessage `json:"configuration"`
}
type Summary struct {
	ID        string    `json:"id"`
	VersionID string    `json:"version_id"`
	OwnerID   int64     `json:"-"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Language  string    `json:"language"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Widget struct {
	Summary
	Source        string          `json:"source"`
	Configuration json.RawMessage `json:"configuration"`
}
type Page struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type Installed struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
	Input
}
type Reference struct {
	WidgetID  string `json:"widget_id"`
	VersionID string `json:"widget_version_id"`
}
type PreviewInput struct {
	DatasetID  string          `json:"dataset_id"`
	Parameters json.RawMessage `json:"parameters"`
}
type PreviewRequest struct {
	JobID     string `json:"job_id"`
	OwnerID   int64  `json:"owner_id"`
	WidgetID  string `json:"widget_id"`
	VersionID string `json:"widget_version_id"`
	Input
	PreviewInput
}
type Job struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}
type Previewer interface {
	Submit(context.Context, PreviewRequest) (Job, error)
}
type Repository interface {
	SyncInstalled(context.Context, []Installed) error
	Create(context.Context, int64, string, string, Input) (Widget, error)
	List(context.Context, int64, bool, string, int) (Page, error)
	Get(context.Context, int64, string) (Widget, error)
	Replace(context.Context, int64, string, string, Input) (Widget, error)
	Delete(context.Context, int64, string) error
	Resolve(context.Context, int64, string, string, bool) (Reference, error)
}
